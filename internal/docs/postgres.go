package docs

import (
	"database/sql"
	"fmt"
	"math/rand"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type Doc struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	UpdatedAt string `json:"updatedAt"`
}

type Service struct {
	db *sql.DB
}

func NewPostgresService(databaseURL string) (*Service, error) {
	dsn := strings.TrimSpace(databaseURL)
	if dsn == "" {
		return nil, fmt.Errorf("database URL is required")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres database: %w", err)
	}

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres database: %w", err)
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
CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    username TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS documents (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    content TEXT NOT NULL DEFAULT '',
    seq_num BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_documents_updated_at
ON documents(updated_at DESC);
`

	if _, err := s.db.Exec(query); err != nil {
		return fmt.Errorf("init postgres schema: %w", err)
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
		`INSERT INTO documents(id, title, content, seq_num) VALUES($1, $2, '', 0)`,
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
		`SELECT id, title, COALESCE(updated_at::text, NOW()::text) FROM documents WHERE id = $1`,
		docID,
	).Scan(&doc.ID, &doc.Title, &doc.UpdatedAt)
	if err != nil {
		return Doc{}, fmt.Errorf("get document: %w", err)
	}
	return doc, nil
}

func (s *Service) UpdateDocumentTitle(docID string, title string) (Doc, error) {
	normalizedTitle := strings.TrimSpace(title)
	if normalizedTitle == "" {
		normalizedTitle = "Untitled Document"
	}

	res, err := s.db.Exec(
		`UPDATE documents
         SET title = $1, updated_at = NOW()
         WHERE id = $2`,
		normalizedTitle,
		docID,
	)
	if err != nil {
		return Doc{}, fmt.Errorf("update document title: %w", err)
	}

	updatedRows, err := res.RowsAffected()
	if err == nil && updatedRows == 0 {
		return Doc{}, fmt.Errorf("update document title: document not found")
	}

	return s.GetDocument(docID)
}

func (s *Service) ListDocuments(limit int) ([]Doc, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := s.db.Query(
		`SELECT id, title, COALESCE(updated_at::text, NOW()::text)
         FROM documents
         ORDER BY updated_at DESC
         LIMIT $1`,
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
		`INSERT INTO documents(id, title, content, seq_num)
         VALUES($1, $2, '', 0)
         ON CONFLICT (id) DO NOTHING`,
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
		`SELECT content, seq_num FROM documents WHERE id = $1`,
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
         SET content = $1, seq_num = $2, updated_at = NOW()
         WHERE id = $3`,
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
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	b := make([]byte, 10)
	for i := range b {
		b[i] = alphabet[rng.Intn(len(alphabet))]
	}
	return "doc-" + string(b)
}

func (s *Service) RegisterUser(username, passwordHash string) error {
	id := generateDocID() // Reuse generator for simplicity
	_, err := s.db.Exec(
		`INSERT INTO users(id, username, password_hash) VALUES($1, $2, $3)`,
		id, username, passwordHash,
	)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key value") {
			return fmt.Errorf("username already exists")
		}
		return fmt.Errorf("register user: %w", err)
	}
	return nil
}

func (s *Service) GetUserHash(username string) (string, error) {
	var hash string
	err := s.db.QueryRow(
		`SELECT password_hash FROM users WHERE username = $1`,
		username,
	).Scan(&hash)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("user not found")
		}
		return "", fmt.Errorf("get user hash: %w", err)
	}
	return hash, nil
}
