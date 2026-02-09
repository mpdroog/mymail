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

// StoreLocal stores an email for local delivery in IMAP-compatible format
// Emails are stored as {mail_dir}/{domain}/INBOX/{timestamp}_{uid}.eml
func (s *Storage) StoreLocal(recipient, from string, data []byte) error {
	domain := getDomain(recipient)

	// Store in domain's INBOX folder (compatible with imapd)
	inboxDir := filepath.Join(s.mailDir, domain, "INBOX")
	if err := os.MkdirAll(inboxDir, 0750); err != nil {
		return err
	}

	// Generate unique filename with .eml extension for imapd compatibility
	uid := s.nextUID(inboxDir)
	filename := fmt.Sprintf("%d_%d.eml", time.Now().Unix(), uid)
	filePath := filepath.Join(inboxDir, filename)

	return os.WriteFile(filePath, data, 0640)
}

// nextUID returns the next available UID for a mailbox
func (s *Storage) nextUID(mailboxPath string) int64 {
	uidFile := filepath.Join(mailboxPath, ".uidnext")
	data, err := os.ReadFile(uidFile)
	uid := int64(1)
	if err == nil {
		if n, parseErr := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &uid); n != 1 || parseErr != nil {
			uid = 1
		}
	}
	os.WriteFile(uidFile, []byte(fmt.Sprintf("%d", uid+1)), 0600)
	return uid
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

func generateQueueID() string {
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), os.Getpid())
}

func getDomain(email string) string {
	parts := strings.Split(email, "@")
	if len(parts) == 2 {
		return parts[1]
	}
	return ""
}
