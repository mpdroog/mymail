package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/mail"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"
)

type Message struct {
	UID      imap.UID
	SeqNum   uint32
	Flags    []imap.Flag
	Date     time.Time
	Size     int64
	Path     string
	From     string
	Subject  string
	raw      []byte
}

type Mailbox struct {
	Name     string
	Messages []*Message
	UIDNext  imap.UID
}

type Storage struct {
	mu        sync.RWMutex
	basePath  string
	whitelist map[string]struct{}
	wlPath    string
}

func NewStorage(basePath, whitelistPath string) (*Storage, error) {
	s := &Storage{
		basePath:  basePath,
		whitelist: make(map[string]struct{}),
		wlPath:    whitelistPath,
	}
	if err := s.LoadWhitelist(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Storage) LoadWhitelist() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	file, err := os.Open(s.wlPath)
	if err != nil {
		if os.IsNotExist(err) {
			s.whitelist = make(map[string]struct{})
			return nil
		}
		return err
	}
	defer file.Close()

	whitelist := make(map[string]struct{})
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		whitelist[strings.ToLower(line)] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	s.whitelist = whitelist
	return nil
}

func (s *Storage) isWhitelisted(from string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.whitelist) == 0 {
		return true // No whitelist = allow all
	}

	addr, err := mail.ParseAddress(from)
	if err != nil {
		return false
	}
	_, ok := s.whitelist[strings.ToLower(addr.Address)]
	return ok
}

func (s *Storage) EnsureMailbox(username, mailbox string) error {
	path := filepath.Join(s.basePath, username, mailbox)
	return os.MkdirAll(path, 0700)
}

func (s *Storage) GetMailbox(username, mailbox string) (*Mailbox, error) {
	path := filepath.Join(s.basePath, username, mailbox)
	if err := os.MkdirAll(path, 0700); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}

	mbox := &Mailbox{
		Name:     mailbox,
		Messages: make([]*Message, 0),
		UIDNext:  1,
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".eml") {
			continue
		}

		msg, err := s.loadMessage(filepath.Join(path, entry.Name()))
		if err != nil {
			continue
		}

		if !s.isWhitelisted(msg.From) {
			continue
		}

		mbox.Messages = append(mbox.Messages, msg)
		if msg.UID >= mbox.UIDNext {
			mbox.UIDNext = msg.UID + 1
		}
	}

	sort.Slice(mbox.Messages, func(i, j int) bool {
		return mbox.Messages[i].UID < mbox.Messages[j].UID
	})

	for i, msg := range mbox.Messages {
		msg.SeqNum = uint32(i + 1)
	}

	return mbox, nil
}

func (s *Storage) loadMessage(path string) (*Message, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	msg, err := mail.ReadMessage(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	uid := parseUIDFromFilename(filepath.Base(path))

	date := info.ModTime()
	if dateStr := msg.Header.Get("Date"); dateStr != "" {
		if t, err := mail.ParseDate(dateStr); err == nil {
			date = t
		}
	}

	flags := s.loadFlags(path)

	return &Message{
		UID:     uid,
		Flags:   flags,
		Date:    date,
		Size:    info.Size(),
		Path:    path,
		From:    msg.Header.Get("From"),
		Subject: msg.Header.Get("Subject"),
		raw:     data,
	}, nil
}

func parseUIDFromFilename(name string) imap.UID {
	name = strings.TrimSuffix(name, ".eml")
	parts := strings.Split(name, "_")
	if len(parts) >= 2 {
		if uid, err := strconv.ParseUint(parts[len(parts)-1], 10, 32); err == nil {
			return imap.UID(uid)
		}
	}
	return 1
}

func (s *Storage) loadFlags(emlPath string) []imap.Flag {
	flagPath := emlPath + ".flags"
	data, err := os.ReadFile(flagPath)
	if err != nil {
		return []imap.Flag{}
	}
	var flags []imap.Flag
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			flags = append(flags, imap.Flag(line))
		}
	}
	return flags
}

func (s *Storage) SaveFlags(emlPath string, flags []imap.Flag) error {
	flagPath := emlPath + ".flags"
	var lines []string
	for _, f := range flags {
		lines = append(lines, string(f))
	}
	return os.WriteFile(flagPath, []byte(strings.Join(lines, "\n")), 0600)
}

func (s *Storage) AppendMessage(username, mailbox string, r io.Reader, size int64, date time.Time) (imap.UID, error) {
	path := filepath.Join(s.basePath, username, mailbox)
	if err := os.MkdirAll(path, 0700); err != nil {
		return 0, err
	}

	uid := s.nextUID(path)
	filename := fmt.Sprintf("%d_%d.eml", date.Unix(), uid)
	fullPath := filepath.Join(path, filename)

	data, err := io.ReadAll(r)
	if err != nil {
		return 0, err
	}

	if err := os.WriteFile(fullPath, data, 0600); err != nil {
		return 0, err
	}

	return uid, nil
}

func (s *Storage) nextUID(mailboxPath string) imap.UID {
	uidFile := filepath.Join(mailboxPath, ".uidnext")
	data, err := os.ReadFile(uidFile)
	uid := imap.UID(1)
	if err == nil {
		if n, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 32); err == nil {
			uid = imap.UID(n)
		}
	}
	os.WriteFile(uidFile, []byte(fmt.Sprintf("%d", uid+1)), 0600)
	return uid
}

func (s *Storage) DeleteMessage(path string) error {
	flagPath := path + ".flags"
	os.Remove(flagPath)
	return os.Remove(path)
}

func (s *Storage) GetRawMessage(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func (s *Storage) ListMailboxes(username string) ([]string, error) {
	path := filepath.Join(s.basePath, username)
	if err := os.MkdirAll(path, 0700); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}

	var mailboxes []string
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			mailboxes = append(mailboxes, entry.Name())
		}
	}

	if len(mailboxes) == 0 {
		os.MkdirAll(filepath.Join(path, "INBOX"), 0700)
		mailboxes = []string{"INBOX"}
	}

	return mailboxes, nil
}

func (s *Storage) ReloadWhitelist() error {
	return s.LoadWhitelist()
}
