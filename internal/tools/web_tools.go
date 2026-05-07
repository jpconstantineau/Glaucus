package tools

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

type ExtractResult struct {
	URL   string `json:"url"`
	Title string `json:"title,omitempty"`
	Text  string `json:"text"`
}

type WebBackend interface {
	Search(ctx context.Context, query string) ([]SearchResult, error)
	Extract(ctx context.Context, rawURL string) (ExtractResult, error)
}

type BrowserToolBackend interface {
	BrowserBackend
	Navigate(ctx context.Context, rawURL string) (map[string]any, error)
	Snapshot(ctx context.Context, target string) (map[string]any, error)
}

type HTTPWebBackend struct {
	client *http.Client
}

type WebSearchTool struct {
	backend WebBackend
}

type WebExtractTool struct {
	backend WebBackend
}

type BrowserNavigateTool struct {
	backend BrowserToolBackend
}

type BrowserSnapshotTool struct {
	backend BrowserToolBackend
}

func NewHTTPWebBackend() *HTTPWebBackend {
	return &HTTPWebBackend{
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

func RegisterWebTools(registry *Registry, webBackend WebBackend, browserBackend BrowserToolBackend) {
	if registry == nil {
		return
	}
	registry.Register(WebSearchTool{backend: webBackend})
	registry.Register(WebExtractTool{backend: webBackend})
	registry.Register(BrowserNavigateTool{backend: browserBackend})
	registry.Register(BrowserSnapshotTool{backend: browserBackend})
}

func (WebSearchTool) Definition() ToolDefinition       { return mustDefinition("web_search") }
func (WebExtractTool) Definition() ToolDefinition      { return mustDefinition("web_extract") }
func (BrowserNavigateTool) Definition() ToolDefinition { return mustDefinition("browser_navigate") }
func (BrowserSnapshotTool) Definition() ToolDefinition { return mustDefinition("browser_snapshot") }

func (t WebSearchTool) CheckAvailability(context.Context, AvailabilityRequest) AvailabilityResult {
	if t.backend == nil {
		return AvailabilityResult{Available: false, Reason: "no web search backend is configured"}
	}
	return AvailabilityResult{Available: true}
}

func (t WebExtractTool) CheckAvailability(context.Context, AvailabilityRequest) AvailabilityResult {
	if t.backend == nil {
		return AvailabilityResult{Available: false, Reason: "no web extraction backend is configured"}
	}
	return AvailabilityResult{Available: true}
}

func (t BrowserNavigateTool) CheckAvailability(ctx context.Context, _ AvailabilityRequest) AvailabilityResult {
	if t.backend == nil {
		return AvailabilityResult{Available: false, Reason: "no browser backend is configured"}
	}
	if err := t.backend.Healthy(ctx); err != nil {
		return AvailabilityResult{Available: false, Reason: fmt.Sprintf("browser backend unavailable: %v", err)}
	}
	return AvailabilityResult{Available: true}
}

func (t BrowserSnapshotTool) CheckAvailability(ctx context.Context, _ AvailabilityRequest) AvailabilityResult {
	if t.backend == nil {
		return AvailabilityResult{Available: false, Reason: "no browser backend is configured"}
	}
	if err := t.backend.Healthy(ctx); err != nil {
		return AvailabilityResult{Available: false, Reason: fmt.Sprintf("browser backend unavailable: %v", err)}
	}
	return AvailabilityResult{Available: true}
}

func (t WebSearchTool) Execute(ctx context.Context, req ToolRequest) ToolResult {
	started := time.Now().UTC()
	if t.backend == nil {
		return fatalResult("web search backend is unavailable", started)
	}

	query, ok := getStringArg(req.Arguments, "query")
	if !ok || strings.TrimSpace(query) == "" {
		return validationResult("query is required", started)
	}

	results, err := t.backend.Search(ctx, query)
	if err != nil {
		return fatalResult(fmt.Sprintf("web search failed: %v", err), started)
	}

	lines := make([]string, 0, len(results))
	for idx, result := range results {
		lines = append(lines, fmt.Sprintf("%d. %s\n%s\n%s", idx+1, result.Title, result.URL, result.Snippet))
	}
	display := strings.TrimSpace(strings.Join(lines, "\n\n"))
	if display == "" {
		display = fmt.Sprintf("No search results for %q.", query)
	}

	return ToolResult{
		Status:      StatusSuccess,
		DisplayText: display,
		Payload: map[string]any{
			"query":   query,
			"results": results,
		},
		Timing: timingSince(started),
	}
}

func (t WebExtractTool) Execute(ctx context.Context, req ToolRequest) ToolResult {
	started := time.Now().UTC()
	if t.backend == nil {
		return fatalResult("web extraction backend is unavailable", started)
	}

	rawURL, ok := getStringArg(req.Arguments, "url")
	if !ok || strings.TrimSpace(rawURL) == "" {
		return validationResult("url is required", started)
	}

	result, err := t.backend.Extract(ctx, rawURL)
	if err != nil {
		return fatalResult(fmt.Sprintf("web extract failed: %v", err), started)
	}

	display := strings.TrimSpace(result.Text)
	if display == "" {
		display = fmt.Sprintf("No extractable text found at %s.", result.URL)
	}

	return ToolResult{
		Status:      StatusSuccess,
		DisplayText: display,
		Payload:     result,
		Timing:      timingSince(started),
	}
}

func (t BrowserNavigateTool) Execute(ctx context.Context, req ToolRequest) ToolResult {
	started := time.Now().UTC()
	if t.backend == nil {
		return fatalResult("browser backend is unavailable", started)
	}

	rawURL, ok := getStringArg(req.Arguments, "url")
	if !ok || strings.TrimSpace(rawURL) == "" {
		return validationResult("url is required", started)
	}
	result, err := t.backend.Navigate(ctx, rawURL)
	if err != nil {
		return fatalResult(fmt.Sprintf("browser navigate failed: %v", err), started)
	}
	return ToolResult{
		Status:      StatusSuccess,
		DisplayText: fmt.Sprintf("Browser navigated to %s.", rawURL),
		Payload:     result,
		Timing:      timingSince(started),
	}
}

func (t BrowserSnapshotTool) Execute(ctx context.Context, req ToolRequest) ToolResult {
	started := time.Now().UTC()
	if t.backend == nil {
		return fatalResult("browser backend is unavailable", started)
	}

	target, _ := getStringArg(req.Arguments, "target")
	result, err := t.backend.Snapshot(ctx, target)
	if err != nil {
		return fatalResult(fmt.Sprintf("browser snapshot failed: %v", err), started)
	}
	return ToolResult{
		Status:      StatusSuccess,
		DisplayText: "Captured browser snapshot.",
		Payload:     result,
		Timing:      timingSince(started),
	}
}

func (b *HTTPWebBackend) Search(ctx context.Context, query string) ([]SearchResult, error) {
	endpoint := "https://duckduckgo.com/html/?q=" + url.QueryEscape(query)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "Glaucus/1.0")

	response, err := b.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	return parseSearchResults(string(body)), nil
}

func (b *HTTPWebBackend) Extract(ctx context.Context, rawURL string) (ExtractResult, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return ExtractResult{}, err
	}
	request.Header.Set("User-Agent", "Glaucus/1.0")

	response, err := b.client.Do(request)
	if err != nil {
		return ExtractResult{}, err
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return ExtractResult{}, err
	}

	content := string(body)
	title := extractTitle(content)
	text := normalizeWhitespace(stripHTML(content))
	return ExtractResult{
		URL:   rawURL,
		Title: title,
		Text:  text,
	}, nil
}

var (
	resultAnchorPattern = regexp.MustCompile(`(?s)<a[^>]*class="result__a"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	tagPattern          = regexp.MustCompile(`(?s)<[^>]+>`)
	scriptPattern       = regexp.MustCompile(`(?is)<script.*?</script>|<style.*?</style>`)
	titlePattern        = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
)

func parseSearchResults(body string) []SearchResult {
	matches := resultAnchorPattern.FindAllStringSubmatch(body, 5)
	results := make([]SearchResult, 0, len(matches))
	for _, match := range matches {
		if len(match) < 3 {
			continue
		}
		results = append(results, SearchResult{
			Title: normalizeWhitespace(stripHTML(match[2])),
			URL:   html.UnescapeString(match[1]),
		})
	}
	return results
}

func extractTitle(body string) string {
	match := titlePattern.FindStringSubmatch(body)
	if len(match) < 2 {
		return ""
	}
	return normalizeWhitespace(stripHTML(match[1]))
}

func stripHTML(body string) string {
	body = scriptPattern.ReplaceAllString(body, " ")
	body = tagPattern.ReplaceAllString(body, " ")
	return html.UnescapeString(body)
}

func normalizeWhitespace(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}
