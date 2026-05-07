package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jpconstantineau/Glaucus/internal/profile"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

const CollectionMemoryDocuments = "memory_documents"

type Document struct {
	ID        string
	ProfileID string
	Slug      string
	Title     string
	Path      string
	Checksum  string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type WriteInput struct {
	ProfileID    string
	ProfileRoot  string
	Slug         string
	Title        string
	RelativePath string
	Content      string
}

type Service struct {
	app core.App
}

func NewService(app core.App) *Service {
	return &Service{app: app}
}

func (s *Service) WriteDocument(ctx context.Context, input WriteInput) (Document, error) {
	if strings.TrimSpace(input.ProfileID) == "" {
		return Document{}, errors.New("profile id is required")
	}
	if strings.TrimSpace(input.ProfileRoot) == "" {
		return Document{}, errors.New("profile root is required")
	}
	if strings.TrimSpace(input.Slug) == "" {
		return Document{}, errors.New("slug is required")
	}
	if strings.TrimSpace(input.Title) == "" {
		input.Title = strings.ReplaceAll(input.Slug, "-", " ")
	}
	if strings.TrimSpace(input.RelativePath) == "" {
		input.RelativePath = filepath.Join("memories", input.Slug+".md")
	}

	resolvedPath, err := profile.ResolveOwnedPath(input.ProfileRoot, input.RelativePath)
	if err != nil {
		return Document{}, err
	}
	if err := os.MkdirAll(filepath.Dir(resolvedPath), 0o755); err != nil {
		return Document{}, fmt.Errorf("ensure memory directory: %w", err)
	}
	if err := os.WriteFile(resolvedPath, []byte(input.Content), 0o644); err != nil {
		return Document{}, fmt.Errorf("write memory file: %w", err)
	}

	checksum := checksum(input.Content)
	existing, _ := s.app.FindFirstRecordByFilter(
		CollectionMemoryDocuments,
		"profile_id = {:profile_id} && slug = {:slug}",
		dbx.Params{"profile_id": input.ProfileID, "slug": input.Slug},
	)

	var record *core.Record
	if existing != nil {
		record = existing
	} else {
		record, err = s.newRecord()
		if err != nil {
			return Document{}, err
		}
		record.Set("profile_id", input.ProfileID)
		record.Set("slug", input.Slug)
	}
	record.Set("title", input.Title)
	record.Set("path", filepath.ToSlash(input.RelativePath))
	record.Set("checksum", checksum)

	if err := s.app.SaveWithContext(ctx, record); err != nil {
		return Document{}, fmt.Errorf("save memory document: %w", err)
	}
	return documentFromRecord(record)
}

func (s *Service) GetDocumentBySlug(ctx context.Context, profileID, slug string) (Document, error) {
	record, err := s.app.FindFirstRecordByFilter(
		CollectionMemoryDocuments,
		"profile_id = {:profile_id} && slug = {:slug}",
		dbx.Params{"profile_id": profileID, "slug": slug},
	)
	if err != nil {
		return Document{}, fmt.Errorf("find memory document: %w", err)
	}
	_ = ctx
	return documentFromRecord(record)
}

func (s *Service) ListDocuments(ctx context.Context, profileID string, limit int) ([]Document, error) {
	if strings.TrimSpace(profileID) == "" {
		return nil, errors.New("profile id is required")
	}
	records, err := s.app.FindRecordsByFilter(
		CollectionMemoryDocuments,
		"profile_id = {:profile_id}",
		"title",
		limit,
		0,
		dbx.Params{"profile_id": profileID},
	)
	if err != nil {
		return nil, fmt.Errorf("list memory documents: %w", err)
	}
	result := make([]Document, 0, len(records))
	for _, record := range records {
		doc, err := documentFromRecord(record)
		if err != nil {
			return nil, err
		}
		result = append(result, doc)
	}
	_ = ctx
	return result, nil
}

func (s *Service) ReadDocumentContent(profileRoot string, doc Document) (string, error) {
	resolvedPath, err := profile.ResolveOwnedPath(profileRoot, doc.Path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return "", fmt.Errorf("read memory file: %w", err)
	}
	return string(data), nil
}

func (s *Service) newRecord() (*core.Record, error) {
	collection, err := s.app.FindCollectionByNameOrId(CollectionMemoryDocuments)
	if err != nil {
		return nil, fmt.Errorf("find memory documents collection: %w", err)
	}
	return core.NewRecord(collection), nil
}

func documentFromRecord(record *core.Record) (Document, error) {
	return Document{
		ID:        record.Id,
		ProfileID: record.GetString("profile_id"),
		Slug:      record.GetString("slug"),
		Title:     record.GetString("title"),
		Path:      record.GetString("path"),
		Checksum:  record.GetString("checksum"),
		CreatedAt: record.GetDateTime("created").Time(),
		UpdatedAt: record.GetDateTime("updated").Time(),
	}, nil
}

func checksum(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
