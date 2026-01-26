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
	helo       string
	mailFrom   string
	rcptTo     []string
	data       []byte
	tls        bool
	auth       bool

	// Server reference
	server     *Server
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

		switch strings.ToUpper(cmd) {
		case "HELO":
			s.handleHELO(arg)
		case "EHLO":
			s.handleEHLO(arg)
		case "MAIL":
			s.handleMAIL(arg)
		case "RCPT":
			s.handleRCPT(arg)
		case "DATA":
			s.handleDATA()
		case "RSET":
			s.handleRSET()
		case "NOOP":
			s.reply(250, "OK")
		case "QUIT":
			s.reply(221, "Bye")
			return
		case "STARTTLS":
			s.handleSTARTTLS()
		case "AUTH":
			s.handleAUTH(arg)
		default:
			s.reply(502, "Command not implemented")
		}
	}
}

func (s *Session) parseCommand(line string) (cmd, arg string) {
	parts := strings.SplitN(line, " ", 2)
	cmd = parts[0]
	if len(parts) > 1 {
		arg = parts[1]
	}
	return
}

func (s *Session) reply(code int, msg string) {
	s.writer.PrintfLine("%d %s", code, msg)
}

func (s *Session) replyMulti(code int, lines []string) {
	for i, line := range lines {
		if i == len(lines)-1 {
			s.writer.PrintfLine("%d %s", code, line)
		} else {
			s.writer.PrintfLine("%d-%s", code, line)
		}
	}
}

func (s *Session) handleHELO(arg string) {
	if arg == "" {
		s.reply(501, "HELO requires domain argument")
		return
	}
	s.helo = arg
	s.reply(250, fmt.Sprintf("Hello %s", arg))
}

func (s *Session) handleEHLO(arg string) {
	if arg == "" {
		s.reply(501, "EHLO requires domain argument")
		return
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

	if config.C.RequireAuth && s.tls {
		extensions = append(extensions, "AUTH PLAIN LOGIN")
	}

	s.replyMulti(250, extensions)
}

func (s *Session) handleMAIL(arg string) {
	if s.helo == "" {
		s.reply(503, "EHLO/HELO first")
		return
	}

	if config.C.RequireAuth && !s.auth {
		s.reply(530, "Authentication required")
		return
	}

	arg = strings.TrimPrefix(strings.ToUpper(arg), "FROM:")
	arg = strings.TrimSpace(arg)

	// Parse email address
	email := s.extractEmail(arg)
	if email == "" {
		s.reply(501, "Invalid sender address")
		return
	}

	// Check sender whitelist (skip for authenticated users)
	if config.C.EnableWhitelist && !s.auth {
		if !s.isSenderWhitelisted(email) {
			log.Printf("Rejected mail from non-whitelisted sender: %s", email)
			s.reply(550, "Sender not on whitelist. " + config.C.RejectMsg)
			return
		}
	}

	s.mailFrom = email
	s.rcptTo = make([]string, 0)
	s.data = nil

	s.reply(250, "OK")
}

func (s *Session) handleRCPT(arg string) {
	if s.mailFrom == "" {
		s.reply(503, "MAIL first")
		return
	}

	if len(s.rcptTo) >= config.C.MaxRecipients {
		s.reply(452, "Too many recipients")
		return
	}

	arg = strings.TrimPrefix(strings.ToUpper(arg), "TO:")
	arg = strings.TrimSpace(arg)

	email := s.extractEmail(arg)
	if email == "" {
		s.reply(501, "Invalid recipient address")
		return
	}

	// Check if we accept mail for this domain
	domain := s.getDomain(email)
	if !s.isLocalDomain(domain) && !s.auth {
		s.reply(550, "Relay access denied")
		return
	}

	s.rcptTo = append(s.rcptTo, email)
	s.reply(250, "OK")
}

func (s *Session) handleDATA() {
	if len(s.rcptTo) == 0 {
		s.reply(503, "RCPT first")
		return
	}

	s.reply(354, "Start mail input; end with <CRLF>.<CRLF>")

	// Read message data
	data, err := s.readData()
	if err != nil {
		log.Printf("Error reading DATA from %s: %v", s.remoteAddr, err)
		s.reply(451, "Error reading message")
		return
	}

	if int64(len(data)) > config.C.MaxSize {
		s.reply(552, "Message too large")
		return
	}

	s.data = data

	// Process the email
	err = s.server.ProcessEmail(s.mailFrom, s.rcptTo, s.data)
	if err != nil {
		log.Printf("Error processing email: %v", err)
		s.reply(451, "Error processing message")
		return
	}

	s.reply(250, "OK message queued")

	// Reset state
	s.mailFrom = ""
	s.rcptTo = make([]string, 0)
	s.data = nil
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

func (s *Session) handleRSET() {
	s.mailFrom = ""
	s.rcptTo = make([]string, 0)
	s.data = nil
	s.reply(250, "OK")
}

func (s *Session) handleSTARTTLS() {
	if s.tls {
		s.reply(503, "TLS already active")
		return
	}

	if config.C.TLSCert == "" {
		s.reply(502, "TLS not available")
		return
	}

	cert, err := tls.LoadX509KeyPair(config.C.TLSCert, config.C.TLSKey)
	if err != nil {
		log.Printf("TLS cert error: %v", err)
		s.reply(454, "TLS not available")
		return
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
	}

	s.reply(220, "Ready to start TLS")

	tlsConn := tls.Server(s.conn, tlsConfig)
	if err := tlsConn.Handshake(); err != nil {
		log.Printf("TLS handshake error from %s: %v", s.remoteAddr, err)
		return
	}

	s.conn = tlsConn
	s.reader = textproto.NewReader(bufio.NewReader(tlsConn))
	s.writer = textproto.NewWriter(bufio.NewWriter(tlsConn))
	s.tls = true

	// Reset state after STARTTLS
	s.helo = ""
	s.mailFrom = ""
	s.rcptTo = make([]string, 0)
}

func (s *Session) handleAUTH(arg string) {
	if !config.C.RequireAuth {
		s.reply(502, "Authentication not enabled")
		return
	}

	if s.auth {
		s.reply(503, "Already authenticated")
		return
	}

	parts := strings.SplitN(arg, " ", 2)
	mechanism := strings.ToUpper(parts[0])

	switch mechanism {
	case "PLAIN":
		s.handleAuthPlain(parts)
	case "LOGIN":
		s.handleAuthLogin()
	default:
		s.reply(504, "Authentication mechanism not supported")
	}
}

func (s *Session) handleAuthPlain(parts []string) {
	var credentials string

	if len(parts) > 1 {
		credentials = parts[1]
	} else {
		s.reply(334, "")
		line, err := s.reader.ReadLine()
		if err != nil {
			return
		}
		credentials = line
	}

	// Decode and verify credentials
	if s.server.AuthenticatePlain(credentials) {
		s.auth = true
		s.reply(235, "Authentication successful")
	} else {
		s.reply(535, "Authentication failed")
	}
}

func (s *Session) handleAuthLogin() {
	// Request username
	s.reply(334, "VXNlcm5hbWU6") // "Username:" base64
	username, err := s.reader.ReadLine()
	if err != nil {
		return
	}

	// Request password
	s.reply(334, "UGFzc3dvcmQ6") // "Password:" base64
	password, err := s.reader.ReadLine()
	if err != nil {
		return
	}

	if s.server.AuthenticateLogin(username, password) {
		s.auth = true
		s.reply(235, "Authentication successful")
	} else {
		s.reply(535, "Authentication failed")
	}
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

func (s *Session) getDomain(email string) string {
	parts := strings.Split(email, "@")
	if len(parts) == 2 {
		return parts[1]
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
	// Check exact email match
	for _, w := range config.C.WhitelistEmails {
		if strings.EqualFold(w, email) {
			return true
		}
	}

	// Check domain match
	domain := s.getDomain(email)
	for _, d := range config.C.WhitelistDomains {
		if strings.EqualFold(d, domain) {
			return true
		}
	}

	return false
}
