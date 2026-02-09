package server

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/textproto"
	"strings"
	"time"

	"github.com/mpdroog/mymail/smtpd/config"
)

type Session struct {
	conn       net.Conn
	reader     *textproto.Reader
	writer     *textproto.Writer
	remoteAddr string

	// State
	helo     string
	mailFrom string
	rcptTo   []string
	data     []byte
	tls      bool
	auth     bool

	// Server reference
	server *Server
}

func NewSession(conn net.Conn, server *Server) *Session {
	return &Session{
		conn:       conn,
		reader:     textproto.NewReader(bufio.NewReader(conn)),
		writer:     textproto.NewWriter(bufio.NewWriter(conn)),
		remoteAddr: conn.RemoteAddr().String(),
		server:     server,
		rcptTo:     make([]string, 0),
	}
}

func (s *Session) Handle() {
	defer s.conn.Close()

	// Send greeting
	s.reply(220, fmt.Sprintf("%s ESMTP ready", config.C.Hostname))

	for {
		s.conn.SetDeadline(time.Now().Add(5 * time.Minute))

		line, err := s.reader.ReadLine()
		if err != nil {
			if err != io.EOF {
				log.Printf("Read error from %s: %v", s.remoteAddr, err)
			}
			return
		}

		if len(line) == 0 {
			continue
		}

		cmd, arg := s.parseCommand(line)

		var e error
		switch strings.ToUpper(cmd) {
		case "HELO":
			e = s.handleHELO(arg)
		case "EHLO":
			e = s.handleEHLO(arg)
		case "MAIL":
			e = s.handleMAIL(arg)
		case "RCPT":
			e = s.handleRCPT(arg)
		case "DATA":
			e = s.handleDATA()
		case "RSET":
			e = s.handleRSET()
		case "NOOP":
			e = s.reply(250, "OK")
		case "QUIT":
			e = s.reply(221, "Bye")
			return
		case "STARTTLS":
			e = s.handleSTARTTLS()
		case "AUTH":
			e = s.handleAUTH(arg)
		default:
			e = s.reply(502, "Command not implemented")
		}
		if e != nil {
			log.Printf("Process error from %s: %v", s.remoteAddr, e)
			// Throw client out
			return
		}
	}
}

func (s *Session) parseCommand(line string) (cmd, arg string) {
	// TODO: Loose, tighten up?
	parts := strings.SplitN(line, " ", 2)
	cmd = parts[0]
	if len(parts) > 1 {
		arg = parts[1]
	}
	return
}

func (s *Session) reply(code int, msg string) error {
	if e := s.writer.PrintfLine("%d %s", code, msg); e != nil {
		return e
	}
	return nil
}

func (s *Session) replyMulti(code int, lines []string) error {
	var e error
	for i, line := range lines {
		if i == len(lines)-1 {
			e = s.writer.PrintfLine("%d %s", code, line)
		} else {
			e = s.writer.PrintfLine("%d-%s", code, line)
		}
		if e != nil {
			return e
		}
	}
	return nil
}

func (s *Session) handleHELO(arg string) error {
	if arg == "" {
		return s.reply(501, "HELO requires domain argument")
	}
	s.helo = arg
	return s.reply(250, fmt.Sprintf("Hello %s", arg))
}

func (s *Session) handleEHLO(arg string) error {
	if arg == "" {
		return s.reply(501, "EHLO requires domain argument")
	}
	if arg != config.C.Hostname {
		return s.reply(501, "EHLO invalid domain")
	}
	s.helo = arg

	extensions := []string{
		fmt.Sprintf("Hello %s", arg),
		fmt.Sprintf("SIZE %d", config.C.MaxSize),
		"8BITMIME",
		"PIPELINING",
	}

	if !s.tls && config.C.TLSCert != "" {
		extensions = append(extensions, "STARTTLS")
	}

	return s.replyMulti(250, extensions)
}

func (s *Session) handleMAIL(arg string) error {
	if s.helo == "" {
		return s.reply(503, "EHLO/HELO first")
	}

	arg = strings.TrimPrefix(strings.ToUpper(arg), "FROM:")
	arg = strings.TrimSpace(arg)

	// Parse email address
	email := s.extractEmail(arg)
	if email == "" {
		return s.reply(501, "Invalid sender address")
	}

	// Check sender whitelist (skip for authenticated users)
	if config.C.EnableWhitelist && !s.auth {
		if !s.isSenderWhitelisted(email) {
			// TODO: hide behind verbosity?
			// TODO: Some webhook so we can do something with it later?
			log.Printf("Rejected mail from non-whitelisted sender: %s", email)
			return s.reply(550, "Sender not on whitelist. "+config.C.RejectMsg)
		}
	}

	s.mailFrom = email
	s.rcptTo = make([]string, 0)
	s.data = nil

	return s.reply(250, "OK")
}

func (s *Session) handleRCPT(arg string) error {
	if s.mailFrom == "" {
		return s.reply(503, "MAIL first")
	}

	if len(s.rcptTo) >= config.C.MaxRecipients {
		return s.reply(452, "Too many recipients")
	}

	arg = strings.TrimPrefix(strings.ToUpper(arg), "TO:")
	arg = strings.TrimSpace(arg)

	email := s.extractEmail(arg)
	if email == "" {
		return s.reply(501, "Invalid recipient address")
	}

	// Check if we accept mail for this domain
	domain, err := getDomain(email)
	if err != nil {
		log.Printf("handleRCPT::getDomain e=" + err.Error())
		return s.reply(550, "Relay cannot process email")
	}

	if !s.isLocalDomain(domain) && !s.auth {
		return s.reply(550, "Relay access denied")
	}

	s.rcptTo = append(s.rcptTo, email)
	return s.reply(250, "OK")
}

func (s *Session) handleDATA() error {
	if len(s.rcptTo) == 0 {
		return s.reply(503, "RCPT first")
	}

	if e := s.reply(354, "Start mail input; end with <CRLF>.<CRLF>"); e != nil {
		return e
	}

	// Read message data
	data, err := s.readData()
	if err != nil {
		log.Printf("Error reading DATA from %s: %v", s.remoteAddr, err)
		return s.reply(451, "Error reading message")
	}

	if int64(len(data)) > config.C.MaxSize {
		return s.reply(552, fmt.Sprintf("Message too large (limit=%s)", config.C.MaxSizeStr))
	}

	s.data = data

	// Process the email
	err = s.server.ProcessEmail(s.mailFrom, s.rcptTo, s.data, s.auth)
	if err != nil {
		log.Printf("Error processing email: %v", err)
		return s.reply(451, "Error processing message")
	}

	if e := s.reply(250, "OK message queued"); e != nil {
		return e
	}

	// Reset state
	s.mailFrom = ""
	s.rcptTo = make([]string, 0)
	s.data = nil

	return nil
}

func (s *Session) readData() ([]byte, error) {
	var data []byte

	for {
		line, err := s.reader.ReadLineBytes()
		if err != nil {
			return nil, err
		}

		// Check for end of data
		if len(line) == 1 && line[0] == '.' {
			break
		}

		// Remove dot-stuffing
		if len(line) > 1 && line[0] == '.' {
			line = line[1:]
		}

		data = append(data, line...)
		data = append(data, '\r', '\n')
	}

	return data, nil
}

func (s *Session) handleRSET() error {
	s.mailFrom = ""
	s.rcptTo = make([]string, 0)
	s.data = nil
	return s.reply(250, "OK")
}

func (s *Session) handleSTARTTLS() error {
	if s.tls {
		return s.reply(503, "TLS already active")
	}

	if config.C.TLSCert == "" {
		return s.reply(502, "TLS not available")
	}

	cert, err := tls.LoadX509KeyPair(config.C.TLSCert, config.C.TLSKey)
	if err != nil {
		// TODO: Move to config so this is only done once?
		log.Printf("TLS cert error: %v", err)
		return s.reply(454, "TLS not available")
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
	}

	if e := s.reply(220, "Ready to start TLS"); e != nil {
		return e
	}

	tlsConn := tls.Server(s.conn, tlsConfig)
	if err := tlsConn.Handshake(); err != nil {
		return err
	}

	s.conn = tlsConn
	s.reader = textproto.NewReader(bufio.NewReader(tlsConn))
	s.writer = textproto.NewWriter(bufio.NewWriter(tlsConn))
	s.tls = true

	// Reset state after STARTTLS
	s.helo = ""
	s.mailFrom = ""
	s.rcptTo = make([]string, 0)

	return nil
}

func (s *Session) handleAUTH(arg string) error {
	if s.auth {
		return s.reply(503, "Already authenticated")
	}

	parts := strings.SplitN(arg, " ", 2)
	mechanism := strings.ToUpper(parts[0])

	switch mechanism {
	case "PLAIN":
		return s.handleAuthPlain(parts)
	case "LOGIN":
		return s.handleAuthLogin()
	}

	return s.reply(504, "Authentication mechanism not supported")
}

func (s *Session) handleAuthPlain(parts []string) error {
	var credentials string

	if len(parts) > 1 {
		credentials = parts[1]
	} else {
		if e := s.reply(334, ""); e != nil {
			return e
		}
		line, err := s.reader.ReadLine()
		if err != nil {
			return err
		}
		credentials = line
	}

	// Decode and verify credentials
	if s.server.AuthenticatePlain(credentials) {
		s.auth = true
		return s.reply(235, "Authentication successful")
	}

	return s.reply(535, "Authentication failed")
}

func (s *Session) handleAuthLogin() error {
	// Request username
	if e := s.reply(334, "VXNlcm5hbWU6"); e != nil {
		return e // "Username:" base64
	}

	username, err := s.reader.ReadLine()
	if err != nil {
		return err
	}

	// Request password
	if e := s.reply(334, "UGFzc3dvcmQ6"); e != nil {
		return e // "Password:" base64
	}
	password, err := s.reader.ReadLine()
	if err != nil {
		return err
	}

	ok, err := s.server.AuthenticateLogin(username, password)
	log.Printf("handleAuthLogin e=" + err.Error())
	if ok {
		s.auth = true
		return s.reply(235, "Authentication successful")
	}

	return s.reply(535, "Authentication failed")
}

func (s *Session) extractEmail(arg string) string {
	// Handle <email> format
	start := strings.Index(arg, "<")
	end := strings.Index(arg, ">")

	if start != -1 && end != -1 && end > start {
		return strings.ToLower(arg[start+1 : end])
	}

	// Handle plain email
	arg = strings.ToLower(strings.TrimSpace(arg))
	if strings.Contains(arg, "@") {
		return arg
	}

	return ""
}

func (s *Session) isLocalDomain(domain string) bool {
	for _, d := range config.C.LocalDomains {
		if strings.EqualFold(d, domain) {
			return true
		}
	}
	return false
}

func (s *Session) isSenderWhitelisted(email string) bool {
	// Check using suffixmatch
	for _, w := range config.C.WhitelistEmails {
		if strings.HasSuffix(email, w) {
			return true
		}
	}
	return false
}
