package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

const CollectionRunEvents = "agent_run_events"

type RunEvent struct {
	ID         string         `json:"id"`
	RunID      string         `json:"run_id"`
	SessionID  string         `json:"session_id"`
	Type       string         `json:"type"`
	Sequence   int            `json:"sequence"`
	Timestamp  time.Time      `json:"timestamp"`
	Payload    map[string]any `json:"payload"`
	IsTerminal bool           `json:"is_terminal"`
}

type AppendEventInput struct {
	ProfileID  string
	RunID      string
	SessionID  string
	Type       string
	Payload    map[string]any
	IsTerminal bool
}

type EventService struct {
	app         core.App
	mu          sync.RWMutex
	runSubs     map[string]map[chan RunEvent]struct{}
	sessionSubs map[string]map[chan RunEvent]struct{}
	statusSubs  map[chan RunEvent]struct{}
}

func NewEventService(app core.App) *EventService {
	return &EventService{
		app:         app,
		runSubs:     map[string]map[chan RunEvent]struct{}{},
		sessionSubs: map[string]map[chan RunEvent]struct{}{},
		statusSubs:  map[chan RunEvent]struct{}{},
	}
}

func (s *EventService) Append(ctx context.Context, input AppendEventInput) (RunEvent, error) {
	record, err := s.newRecord()
	if err != nil {
		return RunEvent{}, err
	}

	sequence, err := s.nextSequence(input.RunID)
	if err != nil {
		return RunEvent{}, err
	}

	record.Set("profile_id", input.ProfileID)
	record.Set("run_id", input.RunID)
	record.Set("session_id", input.SessionID)
	record.Set("sequence", sequence)
	record.Set("event_type", input.Type)
	record.Set("is_terminal", input.IsTerminal)
	if err := setEventJSON(record, "payload_json", input.Payload); err != nil {
		return RunEvent{}, err
	}
	if err := s.app.SaveWithContext(ctx, record); err != nil {
		return RunEvent{}, fmt.Errorf("save run event: %w", err)
	}

	event, err := runEventFromRecord(record)
	if err != nil {
		return RunEvent{}, err
	}
	s.publish(event)
	return event, nil
}

func (s *EventService) ListRunEvents(ctx context.Context, runID string, afterSequence int) ([]RunEvent, error) {
	filter := "run_id = {:run_id}"
	params := dbx.Params{"run_id": runID}
	if afterSequence > 0 {
		filter += " && sequence > {:sequence}"
		params["sequence"] = afterSequence
	}

	records, err := s.app.FindRecordsByFilter(CollectionRunEvents, filter, "sequence", 0, 0, params)
	if err != nil {
		return nil, fmt.Errorf("list run events: %w", err)
	}

	events := make([]RunEvent, 0, len(records))
	for _, record := range records {
		event, err := runEventFromRecord(record)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	_ = ctx
	return events, nil
}

func (s *EventService) ListSessionEvents(ctx context.Context, sessionID string, afterSequence int) ([]RunEvent, error) {
	filter := "session_id = {:session_id}"
	params := dbx.Params{"session_id": sessionID}
	if afterSequence > 0 {
		filter += " && sequence > {:sequence}"
		params["sequence"] = afterSequence
	}

	records, err := s.app.FindRecordsByFilter(CollectionRunEvents, filter, "created,sequence", 0, 0, params)
	if err != nil {
		return nil, fmt.Errorf("list session events: %w", err)
	}

	events := make([]RunEvent, 0, len(records))
	for _, record := range records {
		event, err := runEventFromRecord(record)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	_ = ctx
	return events, nil
}

func (s *EventService) SubscribeRun(runID string) (<-chan RunEvent, func()) {
	ch := make(chan RunEvent, 8)
	s.mu.Lock()
	if s.runSubs[runID] == nil {
		s.runSubs[runID] = map[chan RunEvent]struct{}{}
	}
	s.runSubs[runID][ch] = struct{}{}
	s.mu.Unlock()

	return ch, func() {
		s.mu.Lock()
		delete(s.runSubs[runID], ch)
		if len(s.runSubs[runID]) == 0 {
			delete(s.runSubs, runID)
		}
		s.mu.Unlock()
		close(ch)
	}
}

func (s *EventService) SubscribeSession(sessionID string) (<-chan RunEvent, func()) {
	ch := make(chan RunEvent, 8)
	s.mu.Lock()
	if s.sessionSubs[sessionID] == nil {
		s.sessionSubs[sessionID] = map[chan RunEvent]struct{}{}
	}
	s.sessionSubs[sessionID][ch] = struct{}{}
	s.mu.Unlock()

	return ch, func() {
		s.mu.Lock()
		delete(s.sessionSubs[sessionID], ch)
		if len(s.sessionSubs[sessionID]) == 0 {
			delete(s.sessionSubs, sessionID)
		}
		s.mu.Unlock()
		close(ch)
	}
}

func (s *EventService) SubscribeStatus() (<-chan RunEvent, func()) {
	ch := make(chan RunEvent, 8)
	s.mu.Lock()
	s.statusSubs[ch] = struct{}{}
	s.mu.Unlock()

	return ch, func() {
		s.mu.Lock()
		delete(s.statusSubs, ch)
		s.mu.Unlock()
		close(ch)
	}
}

func (s *EventService) StatusSnapshot(ctx context.Context) (RunEvent, error) {
	records, err := s.app.FindRecordsByFilter(
		"agent_runs",
		"status = 'queued' || status = 'running'",
		"",
		0,
		0,
	)
	if err != nil {
		return RunEvent{}, fmt.Errorf("load active runs status: %w", err)
	}

	return RunEvent{
		ID:        "status-snapshot",
		Type:      "status.snapshot",
		Sequence:  0,
		Timestamp: time.Now().UTC(),
		Payload: map[string]any{
			"active_runs": len(records),
		},
	}, nil
}

func (s *EventService) nextSequence(runID string) (int, error) {
	records, err := s.app.FindRecordsByFilter(
		CollectionRunEvents,
		"run_id = {:run_id}",
		"-sequence",
		1,
		0,
		dbx.Params{"run_id": runID},
	)
	if err != nil {
		return 0, fmt.Errorf("find next event sequence: %w", err)
	}
	if len(records) == 0 {
		return 1, nil
	}
	return records[0].GetInt("sequence") + 1, nil
}

func (s *EventService) newRecord() (*core.Record, error) {
	collection, err := s.app.FindCollectionByNameOrId(CollectionRunEvents)
	if err != nil {
		return nil, fmt.Errorf("find event collection: %w", err)
	}
	return core.NewRecord(collection), nil
}

func (s *EventService) publish(event RunEvent) {
	s.mu.RLock()
	runTargets := copySubscribers(s.runSubs[event.RunID])
	sessionTargets := copySubscribers(s.sessionSubs[event.SessionID])
	statusTargets := copySubscribers(s.statusSubs)
	s.mu.RUnlock()

	statusEvent := RunEvent{
		ID:        "status-" + event.ID,
		Type:      "status.update",
		Sequence:  event.Sequence,
		Timestamp: event.Timestamp,
		Payload: map[string]any{
			"run_id":      event.RunID,
			"session_id":  event.SessionID,
			"event_type":  event.Type,
			"is_terminal": event.IsTerminal,
		},
	}

	for _, ch := range runTargets {
		nonBlockingSend(ch, event)
	}
	for _, ch := range sessionTargets {
		nonBlockingSend(ch, event)
	}
	for _, ch := range statusTargets {
		nonBlockingSend(ch, statusEvent)
	}
}

func copySubscribers[T comparable](source map[chan RunEvent]T) []chan RunEvent {
	channels := make([]chan RunEvent, 0, len(source))
	for ch := range source {
		channels = append(channels, ch)
	}
	return channels
}

func nonBlockingSend(ch chan RunEvent, event RunEvent) {
	select {
	case ch <- event:
	default:
	}
}

func runEventFromRecord(record *core.Record) (RunEvent, error) {
	event := RunEvent{
		ID:         record.Id,
		RunID:      record.GetString("run_id"),
		SessionID:  record.GetString("session_id"),
		Type:       record.GetString("event_type"),
		Sequence:   record.GetInt("sequence"),
		Timestamp:  record.GetDateTime("created").Time(),
		IsTerminal: record.GetBool("is_terminal"),
	}
	if err := decodeEventJSON(record, "payload_json", &event.Payload); err != nil {
		return RunEvent{}, err
	}
	return event, nil
}

func setEventJSON(record *core.Record, field string, value any) error {
	if value == nil {
		record.Set(field, nil)
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", field, err)
	}
	record.Set(field, string(raw))
	return nil
}

func decodeEventJSON(record *core.Record, field string, target any) error {
	raw := record.GetString(field)
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(raw), target); err != nil {
		return fmt.Errorf("decode %s: %w", field, err)
	}
	return nil
}
