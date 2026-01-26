package main

import (
	"bytes"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
)

type Session struct {
	server   *Server
	username string
	mailbox  *Mailbox
}

func (s *Session) Close() error {
	return nil
}

func (s *Session) Login(username, password string) error {
	if !s.server.users.Validate(username, password) {
		return imapserver.ErrAuthFailed
	}
	s.username = username
	if err := s.server.storage.EnsureMailbox(username, "INBOX"); err != nil {
		return err
	}
	return nil
}

func (s *Session) Select(mailbox string, options *imap.SelectOptions) (*imap.SelectData, error) {
	mbox, err := s.server.storage.GetMailbox(s.username, mailbox)
	if err != nil {
		return nil, err
	}
	s.mailbox = mbox

	flags := []imap.Flag{imap.FlagSeen, imap.FlagAnswered, imap.FlagFlagged, imap.FlagDeleted, imap.FlagDraft}
	permanentFlags := []imap.Flag{imap.FlagSeen, imap.FlagAnswered, imap.FlagFlagged, imap.FlagDeleted, imap.FlagDraft}

	return &imap.SelectData{
		Flags:          flags,
		PermanentFlags: permanentFlags,
		NumMessages:    uint32(len(mbox.Messages)),
		UIDNext:        mbox.UIDNext,
		UIDValidity:    1,
	}, nil
}

func (s *Session) Unselect() error {
	s.mailbox = nil
	return nil
}

func (s *Session) Create(mailbox string, options *imap.CreateOptions) error {
	return s.server.storage.EnsureMailbox(s.username, mailbox)
}

func (s *Session) Delete(mailbox string) error {
	return fmt.Errorf("DELETE not supported")
}

func (s *Session) Rename(mailbox, newName string, options *imap.RenameOptions) error {
	return fmt.Errorf("RENAME not supported")
}

func (s *Session) Subscribe(mailbox string) error {
	return nil
}

func (s *Session) Unsubscribe(mailbox string) error {
	return nil
}

func (s *Session) List(w *imapserver.ListWriter, ref string, patterns []string, options *imap.ListOptions) error {
	mailboxes, err := s.server.storage.ListMailboxes(s.username)
	if err != nil {
		return err
	}

	for _, mbox := range mailboxes {
		for _, pattern := range patterns {
			if matchMailbox(mbox, ref, pattern) {
				w.WriteList(&imap.ListData{
					Mailbox: mbox,
					Delim:   '/',
				})
				break
			}
		}
	}
	return nil
}

func matchMailbox(mailbox, ref, pattern string) bool {
	if pattern == "*" || pattern == "%" {
		return true
	}
	if ref != "" {
		pattern = ref + pattern
	}
	pattern = strings.ReplaceAll(pattern, "*", "")
	pattern = strings.ReplaceAll(pattern, "%", "")
	if pattern == "" {
		return true
	}
	return strings.Contains(strings.ToUpper(mailbox), strings.ToUpper(pattern))
}

func (s *Session) Status(mailbox string, options *imap.StatusOptions) (*imap.StatusData, error) {
	mbox, err := s.server.storage.GetMailbox(s.username, mailbox)
	if err != nil {
		return nil, err
	}

	data := &imap.StatusData{Mailbox: mailbox}
	if options.NumMessages {
		n := uint32(len(mbox.Messages))
		data.NumMessages = &n
	}
	if options.UIDNext {
		data.UIDNext = mbox.UIDNext
	}
	if options.UIDValidity {
		v := uint32(1)
		data.UIDValidity = v
	}
	if options.NumUnseen {
		var unseen uint32
		for _, msg := range mbox.Messages {
			if !hasFlag(msg.Flags, imap.FlagSeen) {
				unseen++
			}
		}
		data.NumUnseen = &unseen
	}
	return data, nil
}

func hasFlag(flags []imap.Flag, flag imap.Flag) bool {
	for _, f := range flags {
		if f == flag {
			return true
		}
	}
	return false
}

func (s *Session) Append(mailbox string, r imap.LiteralReader, options *imap.AppendOptions) (*imap.AppendData, error) {
	date := time.Now()
	if options.Time != (time.Time{}) {
		date = options.Time
	}

	uid, err := s.server.storage.AppendMessage(s.username, mailbox, r, r.Size(), date)
	if err != nil {
		return nil, err
	}

	return &imap.AppendData{
		UID:         uid,
		UIDValidity: 1,
	}, nil
}

func (s *Session) Fetch(w *imapserver.FetchWriter, numSet imap.NumSet, options *imap.FetchOptions) error {
	if s.mailbox == nil {
		return fmt.Errorf("no mailbox selected")
	}

	for _, msg := range s.mailbox.Messages {
		if !numSetContains(numSet, msg.SeqNum, msg.UID) {
			continue
		}

		fw := w.CreateMessage(msg.SeqNum)
		if fw == nil {
			continue
		}

		if options.UID {
			fw.WriteUID(msg.UID)
		}
		if options.Flags {
			fw.WriteFlags(msg.Flags)
		}
		if options.InternalDate {
			fw.WriteInternalDate(msg.Date)
		}
		if options.RFC822Size {
			fw.WriteRFC822Size(msg.Size)
		}
		if options.Envelope {
			env, err := s.getEnvelope(msg)
			if err == nil {
				fw.WriteEnvelope(env)
			}
		}
		if options.BodyStructure != nil {
			bs := s.getBodyStructure(msg)
			fw.WriteBodyStructure(bs)
		}

		for _, bs := range options.BodySection {
			data, err := s.server.storage.GetRawMessage(msg.Path)
			if err != nil {
				continue
			}

			wc := fw.WriteBodySection(bs, int64(len(data)))
			wc.Write(data)
			wc.Close()

			if !bs.Peek && !hasFlag(msg.Flags, imap.FlagSeen) {
				msg.Flags = append(msg.Flags, imap.FlagSeen)
				s.server.storage.SaveFlags(msg.Path, msg.Flags)
			}
		}

		fw.Close()
	}
	return nil
}

func numSetContains(numSet imap.NumSet, seqNum uint32, uid imap.UID) bool {
	switch ns := numSet.(type) {
	case imap.SeqSet:
		return ns.Contains(seqNum)
	case imap.UIDSet:
		return ns.Contains(uid)
	}
	return false
}

func (s *Session) getEnvelope(msg *Message) (*imap.Envelope, error) {
	data, err := s.server.storage.GetRawMessage(msg.Path)
	if err != nil {
		return nil, err
	}

	m, err := mail.ReadMessage(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	env := &imap.Envelope{
		Subject: m.Header.Get("Subject"),
		Date:    msg.Date,
	}

	if from := m.Header.Get("From"); from != "" {
		env.From = parseAddresses(from)
	}
	if to := m.Header.Get("To"); to != "" {
		env.To = parseAddresses(to)
	}
	if cc := m.Header.Get("Cc"); cc != "" {
		env.Cc = parseAddresses(cc)
	}
	if replyTo := m.Header.Get("Reply-To"); replyTo != "" {
		env.ReplyTo = parseAddresses(replyTo)
	}
	env.MessageID = m.Header.Get("Message-Id")
	if inReplyTo := m.Header.Get("In-Reply-To"); inReplyTo != "" {
		env.InReplyTo = []string{inReplyTo}
	}

	return env, nil
}

func parseAddresses(s string) []imap.Address {
	addrs, err := mail.ParseAddressList(s)
	if err != nil {
		return nil
	}
	var result []imap.Address
	for _, addr := range addrs {
		parts := strings.SplitN(addr.Address, "@", 2)
		mailbox := addr.Address
		host := ""
		if len(parts) == 2 {
			mailbox = parts[0]
			host = parts[1]
		}
		result = append(result, imap.Address{
			Name:    addr.Name,
			Mailbox: mailbox,
			Host:    host,
		})
	}
	return result
}

func (s *Session) getBodyStructure(msg *Message) imap.BodyStructure {
	return &imap.BodyStructureSinglePart{
		Type:    "text",
		Subtype: "plain",
		Params:  map[string]string{"charset": "utf-8"},
		Size:    uint32(msg.Size),
	}
}

func (s *Session) Search(kind imapserver.NumKind, criteria *imap.SearchCriteria, options *imap.SearchOptions) (*imap.SearchData, error) {
	if s.mailbox == nil {
		return nil, fmt.Errorf("no mailbox selected")
	}

	var uids []imap.UID

	for _, msg := range s.mailbox.Messages {
		if s.matchesCriteria(msg, criteria) {
			uids = append(uids, msg.UID)
		}
	}

	data := &imap.SearchData{}
	if kind == imapserver.NumKindUID {
		var uidSet imap.UIDSet
		for _, uid := range uids {
			uidSet.AddNum(uid)
		}
		data.All = uidSet
	} else {
		var seqSet imap.SeqSet
		for _, msg := range s.mailbox.Messages {
			for _, uid := range uids {
				if msg.UID == uid {
					seqSet.AddNum(msg.SeqNum)
					break
				}
			}
		}
		data.All = seqSet
	}

	return data, nil
}

func (s *Session) matchesCriteria(msg *Message, criteria *imap.SearchCriteria) bool {
	if criteria == nil {
		return true
	}

	for _, flag := range criteria.Flag {
		if !hasFlag(msg.Flags, flag) {
			return false
		}
	}

	for _, flag := range criteria.NotFlag {
		if hasFlag(msg.Flags, flag) {
			return false
		}
	}

	if !criteria.Since.IsZero() && msg.Date.Before(criteria.Since) {
		return false
	}

	if !criteria.Before.IsZero() && msg.Date.After(criteria.Before) {
		return false
	}

	return true
}

func (s *Session) Store(w *imapserver.FetchWriter, numSet imap.NumSet, flags *imap.StoreFlags, options *imap.StoreOptions) error {
	if s.mailbox == nil {
		return fmt.Errorf("no mailbox selected")
	}

	for _, msg := range s.mailbox.Messages {
		if !numSetContains(numSet, msg.SeqNum, msg.UID) {
			continue
		}

		switch flags.Op {
		case imap.StoreFlagsSet:
			msg.Flags = flags.Flags
		case imap.StoreFlagsAdd:
			for _, f := range flags.Flags {
				if !hasFlag(msg.Flags, f) {
					msg.Flags = append(msg.Flags, f)
				}
			}
		case imap.StoreFlagsDel:
			var newFlags []imap.Flag
			for _, f := range msg.Flags {
				remove := false
				for _, rf := range flags.Flags {
					if f == rf {
						remove = true
						break
					}
				}
				if !remove {
					newFlags = append(newFlags, f)
				}
			}
			msg.Flags = newFlags
		}

		s.server.storage.SaveFlags(msg.Path, msg.Flags)

		if !flags.Silent {
			fw := w.CreateMessage(msg.SeqNum)
			if fw != nil {
				fw.WriteFlags(msg.Flags)
				fw.Close()
			}
		}
	}
	return nil
}

func (s *Session) Copy(numSet imap.NumSet, dest string) (*imap.CopyData, error) {
	if s.mailbox == nil {
		return nil, fmt.Errorf("no mailbox selected")
	}

	var srcUIDs imap.UIDSet
	var destUIDs imap.UIDSet

	for _, msg := range s.mailbox.Messages {
		if !numSetContains(numSet, msg.SeqNum, msg.UID) {
			continue
		}

		data, err := s.server.storage.GetRawMessage(msg.Path)
		if err != nil {
			continue
		}

		uid, err := s.server.storage.AppendMessage(s.username, dest, bytes.NewReader(data), int64(len(data)), msg.Date)
		if err != nil {
			continue
		}

		srcUIDs.AddNum(msg.UID)
		destUIDs.AddNum(uid)
	}

	return &imap.CopyData{
		UIDValidity: 1,
		SourceUIDs:  srcUIDs,
		DestUIDs:    destUIDs,
	}, nil
}

func (s *Session) Expunge(w *imapserver.ExpungeWriter, uids *imap.UIDSet) error {
	if s.mailbox == nil {
		return fmt.Errorf("no mailbox selected")
	}

	var toDelete []*Message
	for _, msg := range s.mailbox.Messages {
		if !hasFlag(msg.Flags, imap.FlagDeleted) {
			continue
		}
		if uids != nil && !uids.Contains(msg.UID) {
			continue
		}
		toDelete = append(toDelete, msg)
	}

	for i := len(toDelete) - 1; i >= 0; i-- {
		msg := toDelete[i]
		if err := s.server.storage.DeleteMessage(msg.Path); err != nil {
			continue
		}
		if w != nil {
			w.WriteExpunge(msg.SeqNum)
		}
	}

	return nil
}

func (s *Session) Poll(w *imapserver.UpdateWriter, allowExpunge bool) error {
	return nil
}

func (s *Session) Idle(w *imapserver.UpdateWriter, stop <-chan struct{}) error {
	<-stop
	return nil
}

func (s *Session) Namespace() (*imap.NamespaceData, error) {
	return &imap.NamespaceData{
		Personal: []imap.NamespaceDescriptor{{Delim: '/'}},
	}, nil
}

type Server struct {
	users   *UserStore
	storage *Storage
}

func NewServer(users *UserStore, storage *Storage) *Server {
	return &Server{
		users:   users,
		storage: storage,
	}
}

func (srv *Server) NewSession() *Session {
	return &Session{server: srv}
}
