package docs

import (
	"database/sql"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Doc struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	UpdatedAt string `json:"updatedAt"`
}

type Service struct {
	db *sql.DB
}

func NewSQLiteService(dbPath string) (*Service, error) {
	if dbPath == "" {
		dbPath = filepath.Join("data", "syncwave.db")
	}

	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, fmt.Errorf("create sqlite directory: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}

	s := &Service{db: db}
	if err := s.initSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}

	return s, nil
}

func (s *Service) initSchema() error {
	query := `
CREATE TABLE IF NOT EXISTS documents (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    content TEXT NOT NULL DEFAULT '',
    seq_num INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_documents_updated_at
ON documents(updated_at DESC);
`

	if _, err := s.db.Exec(query); err != nil {
		return fmt.Errorf("init sqlite schema: %w", err)
	}
	return nil
}

func (s *Service) CreateDocument(title string) (Doc, error) {
	normalizedTitle := strings.TrimSpace(title)
	if normalizedTitle == "" {
		normalizedTitle = "Untitled Document"
	}

	id := generateDocID()
	if _, err := s.db.Exec(
		`INSERT INTO documents(id, title, content, seq_num) VALUES(?, ?, '', 0)`,
		id,
		normalizedTitle,
	); err != nil {
		return Doc{}, fmt.Errorf("create document: %w", err)
	}

	return s.GetDocument(id)
}

func (s *Service) GetDocument(docID string) (Doc, error) {
	var doc Doc
	err := s.db.QueryRow(
		`SELECT id, title, COALESCE(updated_at, CURRENT_TIMESTAMP) FROM documents WHERE id = ?`,
		docID,
	).Scan(&doc.ID, &doc.Title, &doc.UpdatedAt)
	if err != nil {
		return Doc{}, fmt.Errorf("get document: %w", err)
	}
	return doc, nil
}

func (s *Service) ListDocuments(limit int) ([]Doc, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := s.db.Query(
		`SELECT id, title, COALESCE(updated_at, CURRENT_TIMESTAMP)
         FROM documents
         ORDER BY updated_at DESC
         LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list documents: %w", err)
	}
	defer rows.Close()

	docs := make([]Doc, 0)
	for rows.Next() {
		var doc Doc
		if err := rows.Scan(&doc.ID, &doc.Title, &doc.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan document: %w", err)
		}
		docs = append(docs, doc)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate documents: %w", err)
	}

	return docs, nil
}

func (s *Service) EnsureDocument(docID string, title string) error {
	normalizedTitle := strings.TrimSpace(title)
	if normalizedTitle == "" {
		normalizedTitle = "Untitled Document"
	}

	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO documents(id, title, content, seq_num) VALUES(?, ?, '', 0)`,
		docID,
		normalizedTitle,
	)
	if err != nil {
		return fmt.Errorf("ensure document: %w", err)
	}
	return nil
}

func (s *Service) LoadStateOrCreate(docID string) (content string, seq int, err error) {
	if err := s.EnsureDocument(docID, "Untitled Document"); err != nil {
		return "", 0, err
	}

	err = s.db.QueryRow(
		`SELECT content, seq_num FROM documents WHERE id = ?`,
		docID,
	).Scan(&content, &seq)
	if err != nil {
		return "", 0, fmt.Errorf("load document state: %w", err)
	}

	return content, seq, nil
}

func (s *Service) SaveState(docID string, content string, seq int) error {
	_, err := s.db.Exec(
		`UPDATE documents
         SET content = ?, seq_num = ?, updated_at = CURRENT_TIMESTAMP
         WHERE id = ?`,
		content,
		seq,
		docID,
	)
	if err != nil {
		return fmt.Errorf("save document state: %w", err)
	}
	return nil
}

func generateDocID() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	rand.Seed(time.Now().UnixNano())
	b := make([]byte, 10)
	for i := range b {
		b[i] = alphabet[rand.Intn(len(alphabet))]
	}
	return "doc-" + string(b)
}
