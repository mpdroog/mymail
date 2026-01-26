package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mpdroog/mymail/smtpd/config"
)

type Storage struct {
	mailDir  string
	queueDir string
}

type QueuedEmail struct {
	ID        string    `json:"id"`
	From      string    `json:"from"`
	To        string    `json:"to"`
	Data      []byte    `json:"data"`
	CreatedAt time.Time `json:"created_at"`
	Attempts  int       `json:"attempts"`
	LastError string    `json:"last_error"`
	NextRetry time.Time `json:"next_retry"`
}

func New() *Storage {
	return &Storage{
		mailDir:  config.C.MailDir,
		queueDir: config.C.QueueDir,
	}
}

func (s *Storage) Init() error {
	// Create mail directory
	if err := os.MkdirAll(s.mailDir, 0750); err != nil {
		return fmt.Errorf("failed to create mail dir: %v", err)
	}

	// Create queue directory
	if err := os.MkdirAll(s.queueDir, 0750); err != nil {
		return fmt.Errorf("failed to create queue dir: %v", err)
	}

	return nil
}

// StoreLocal stores an email for local delivery using Maildir format
func (s *Storage) StoreLocal(recipient, from string, data []byte) error {
	// Extract local part of email for directory
	localPart := getLocalPart(recipient)
	domain := getDomain(recipient)

	// Create user maildir structure
	userDir := filepath.Join(s.mailDir, domain, localPart)
	newDir := filepath.Join(userDir, "new")
	curDir := filepath.Join(userDir, "cur")
	tmpDir := filepath.Join(userDir, "tmp")

	for _, dir := range []string{newDir, curDir, tmpDir} {
		if err := os.MkdirAll(dir, 0750); err != nil {
			return err
		}
	}

	// Generate unique filename
	filename := generateMaildirFilename()

	// Write to tmp first
	tmpPath := filepath.Join(tmpDir, filename)
	if err := os.WriteFile(tmpPath, data, 0640); err != nil {
		return err
	}

	// Move to new
	newPath := filepath.Join(newDir, filename)
	return os.Rename(tmpPath, newPath)
}

// QueueForRelay adds an email to the outgoing queue
func (s *Storage) QueueForRelay(from, to string, data []byte) error {
	email := QueuedEmail{
		ID:        generateQueueID(),
		From:      from,
		To:        to,
		Data:      data,
		CreatedAt: time.Now(),
		Attempts:  0,
		NextRetry: time.Now(),
	}

	filename := filepath.Join(s.queueDir, email.ID+".json")

	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	return encoder.Encode(&email)
}

// GetQueuedEmails returns all emails ready for delivery
func (s *Storage) GetQueuedEmails() ([]QueuedEmail, error) {
	var emails []QueuedEmail

	entries, err := os.ReadDir(s.queueDir)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		path := filepath.Join(s.queueDir, entry.Name())
		email, err := s.loadQueuedEmail(path)
		if err != nil {
			continue
		}

		if email.NextRetry.Before(now) || email.NextRetry.Equal(now) {
			emails = append(emails, *email)
		}
	}

	return emails, nil
}

func (s *Storage) loadQueuedEmail(path string) (*QueuedEmail, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var email QueuedEmail
	if err := json.NewDecoder(f).Decode(&email); err != nil {
		return nil, err
	}

	return &email, nil
}

// UpdateQueuedEmail updates a queued email after a delivery attempt
func (s *Storage) UpdateQueuedEmail(email *QueuedEmail) error {
	filename := filepath.Join(s.queueDir, email.ID+".json")

	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	return encoder.Encode(email)
}

// RemoveFromQueue removes an email from the queue
func (s *Storage) RemoveFromQueue(id string) error {
	filename := filepath.Join(s.queueDir, id+".json")
	return os.Remove(filename)
}

func generateMaildirFilename() string {
	hostname, _ := os.Hostname()
	return fmt.Sprintf("%d.%d.%s", time.Now().Unix(), os.Getpid(), hostname)
}

func generateQueueID() string {
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), os.Getpid())
}

func getLocalPart(email string) string {
	parts := strings.Split(email, "@")
	if len(parts) >= 1 {
		return parts[0]
	}
	return ""
}

func getDomain(email string) string {
	parts := strings.Split(email, "@")
	if len(parts) == 2 {
		return parts[1]
	}
	return ""
}
