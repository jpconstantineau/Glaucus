package app

import (
	"context"

	"github.com/jpconstantineau/Glaucus/internal/memory"
	"github.com/jpconstantineau/Glaucus/internal/search"
	"github.com/jpconstantineau/Glaucus/internal/sessions"
	"github.com/jpconstantineau/Glaucus/internal/tools"
)

type todoToolAdapter struct {
	service *sessions.Service
}

func (a todoToolAdapter) GetSessionTodos(ctx context.Context, sessionID string) ([]map[string]any, error) {
	session, err := a.service.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return session.Todo, nil
}

func (a todoToolAdapter) ReplaceSessionTodos(ctx context.Context, sessionID string, items []map[string]any) ([]map[string]any, error) {
	session, err := a.service.ReplaceSessionTodo(ctx, sessionID, items)
	if err != nil {
		return nil, err
	}
	return session.Todo, nil
}

type memoryToolAdapter struct {
	service     *memory.Service
	profileRoot string
}

func (a memoryToolAdapter) ListMemoryDocuments(ctx context.Context, profileID string, limit int) (any, error) {
	return a.service.ListDocuments(ctx, profileID, limit)
}

func (a memoryToolAdapter) ViewMemoryDocument(ctx context.Context, profileID, slug string) (any, string, error) {
	doc, err := a.service.GetDocumentBySlug(ctx, profileID, slug)
	if err != nil {
		return nil, "", err
	}
	content, err := a.service.ReadDocumentContent(a.profileRoot, doc)
	if err != nil {
		return nil, "", err
	}
	return doc, content, nil
}

func (a memoryToolAdapter) WriteMemoryDocument(ctx context.Context, input tools.MemoryWriteInput) (any, error) {
	return a.service.WriteDocument(ctx, memory.WriteInput{
		ProfileID:    input.ProfileID,
		ProfileRoot:  input.ProfileRoot,
		Slug:         input.Slug,
		Title:        input.Title,
		RelativePath: input.RelativePath,
		Content:      input.Content,
	})
}

type searchToolAdapter struct {
	service *search.Service
}

func (a searchToolAdapter) SearchSessions(ctx context.Context, profileID, query string, limit int) (any, error) {
	return a.service.SearchSessions(ctx, profileID, query, limit)
}
