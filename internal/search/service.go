package search

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jpconstantineau/Glaucus/internal/sessions"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

type Result struct {
	ObjectType   string    `json:"object_type"`
	ObjectID     string    `json:"object_id"`
	Title        string    `json:"title"`
	Snippet      string    `json:"snippet"`
	MatchedField string    `json:"matched_field"`
	ProfileID    string    `json:"profile_id"`
	SessionID    string    `json:"session_id"`
	MessageID    string    `json:"message_id"`
	Timestamp    time.Time `json:"timestamp"`
}

type Service struct {
	app      core.App
	sessions *sessions.Service
}

func NewService(app core.App, sessionService *sessions.Service) *Service {
	return &Service{app: app, sessions: sessionService}
}

func (s *Service) RebuildSessionIndex(ctx context.Context, profileID string) error {
	if strings.TrimSpace(profileID) == "" {
		return fmt.Errorf("profile id is required")
	}
	if _, err := s.app.NonconcurrentDB().NewQuery("DELETE FROM session_search_fts WHERE profile_id = {:profile_id}").Bind(dbx.Params{"profile_id": profileID}).Execute(); err != nil {
		return fmt.Errorf("clear session search index: %w", err)
	}

	sessionRows, err := s.app.FindRecordsByFilter(
		sessions.CollectionSessions,
		"profile_id = {:profile_id}",
		"",
		0,
		0,
		dbx.Params{"profile_id": profileID},
	)
	if err != nil {
		return fmt.Errorf("list sessions for search: %w", err)
	}
	for _, sessionRow := range sessionRows {
		messages, err := s.sessions.ListMessages(ctx, sessionRow.Id)
		if err != nil {
			return fmt.Errorf("list messages for search: %w", err)
		}
		for _, message := range messages {
			payload := dbx.Params{
				"profile_id":   profileID,
				"session_id":   sessionRow.Id,
				"message_id":   message.ID,
				"title":        sessionRow.GetString("title"),
				"body":         message.VisibleText,
				"visible_text": message.VisibleText,
			}
			if _, err := s.app.NonconcurrentDB().NewQuery(`
				INSERT INTO session_search_fts(profile_id, session_id, message_id, title, body, visible_text)
				VALUES ({:profile_id}, {:session_id}, {:message_id}, {:title}, {:body}, {:visible_text})
			`).Bind(payload).Execute(); err != nil {
				return fmt.Errorf("insert session search row: %w", err)
			}
		}
	}
	return nil
}

func (s *Service) SearchSessions(ctx context.Context, profileID, query string, limit int) ([]Result, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("query is required")
	}
	if err := s.RebuildSessionIndex(ctx, profileID); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 10
	}

	rows := []struct {
		ProfileID   string `db:"profile_id"`
		SessionID   string `db:"session_id"`
		MessageID   string `db:"message_id"`
		Title       string `db:"title"`
		VisibleText string `db:"visible_text"`
		Snippet     string `db:"snippet"`
	}{}

	if err := s.app.DB().NewQuery(`
		SELECT
			fts.profile_id,
			fts.session_id,
			fts.message_id,
			fts.title,
			fts.visible_text,
			snippet(session_search_fts, 4, '[', ']', '...', 10) AS snippet
		FROM session_search_fts fts
		WHERE session_search_fts MATCH {:query} AND fts.profile_id = {:profile_id}
		ORDER BY bm25(session_search_fts)
		LIMIT ` + fmt.Sprintf("%d", limit)).Bind(dbx.Params{
		"query":      query,
		"profile_id": profileID,
	}).All(&rows); err != nil {
		return nil, fmt.Errorf("search sessions: %w", err)
	}

	results := make([]Result, 0, len(rows))
	for _, row := range rows {
		results = append(results, Result{
			ObjectType:   "session_message",
			ObjectID:     row.MessageID,
			Title:        row.Title,
			Snippet:      row.Snippet,
			MatchedField: "visible_text",
			ProfileID:    row.ProfileID,
			SessionID:    row.SessionID,
			MessageID:    row.MessageID,
		})
	}
	return results, nil
}
