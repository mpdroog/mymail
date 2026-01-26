package server

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"log"
	"net"
	"os"
	"strings"
	"sync"

	"github.com/mpdroog/mymail/smtpd/config"
	"github.com/mpdroog/mymail/smtpd/storage"
)

type Server struct {
	listener net.Listener
	wg       sync.WaitGroup
	quit     chan struct{}
	users    map[string]string // username -> password
	storage  *storage.Storage
}

func New() *Server {
	return &Server{
		quit:  make(chan struct{}),
		users: make(map[string]string),
	}
}

func (s *Server) LoadUsers(path string) error {
	if path == "" {
		return nil
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	return json.NewDecoder(f).Decode(&s.users)
}

func (s *Server) SetStorage(st *storage.Storage) {
	s.storage = st
}

func (s *Server) Start() error {
	var err error
	var listener net.Listener

	if config.C.TLSCert != "" && config.C.TLSKey != "" {
		// Try to load TLS config for implicit TLS (port 465)
		cert, err := tls.LoadX509KeyPair(config.C.TLSCert, config.C.TLSKey)
		if err != nil {
			log.Printf("Warning: Could not load TLS cert, starting without implicit TLS: %v", err)
			listener, err = net.Listen("tcp", config.C.ListenAddr)
		} else {
			tlsConfig := &tls.Config{
				Certificates: []tls.Certificate{cert},
			}
			listener, err = tls.Listen("tcp", config.C.ListenAddr, tlsConfig)
		}
	} else {
		listener, err = net.Listen("tcp", config.C.ListenAddr)
	}

	if err != nil {
		return err
	}

	s.listener = listener
	log.Printf("SMTP server listening on %s", config.C.ListenAddr)

	go s.acceptLoop()

	return nil
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.quit:
				return
			default:
				log.Printf("Accept error: %v", err)
				continue
			}
		}

		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			session := NewSession(conn, s)
			session.Handle()
		}()
	}
}

func (s *Server) Stop() {
	close(s.quit)
	s.listener.Close()
	s.wg.Wait()
	log.Println("SMTP server stopped")
}

func (s *Server) ProcessEmail(from string, to []string, data []byte) error {
	// Determine if this is local delivery or needs to be relayed
	for _, recipient := range to {
		domain := getDomain(recipient)

		if s.isLocalDomain(domain) {
			// Local delivery
			if err := s.storage.StoreLocal(recipient, from, data); err != nil {
				return err
			}
		} else {
			// Queue for relay
			if err := s.storage.QueueForRelay(from, recipient, data); err != nil {
				return err
			}
		}
	}

	return nil
}

func (s *Server) AuthenticatePlain(credentials string) bool {
	decoded, err := base64.StdEncoding.DecodeString(credentials)
	if err != nil {
		return false
	}

	// PLAIN format: \0username\0password
	parts := strings.Split(string(decoded), "\x00")
	if len(parts) != 3 {
		return false
	}

	username := parts[1]
	password := parts[2]

	storedPass, ok := s.users[username]
	return ok && storedPass == password
}

func (s *Server) AuthenticateLogin(usernameB64, passwordB64 string) bool {
	username, err := base64.StdEncoding.DecodeString(usernameB64)
	if err != nil {
		return false
	}

	password, err := base64.StdEncoding.DecodeString(passwordB64)
	if err != nil {
		return false
	}

	storedPass, ok := s.users[string(username)]
	return ok && storedPass == string(password)
}

func (s *Server) isLocalDomain(domain string) bool {
	for _, d := range config.C.LocalDomains {
		if strings.EqualFold(d, domain) {
			return true
		}
	}
	return false
}

func getDomain(email string) string {
	parts := strings.Split(email, "@")
	if len(parts) == 2 {
		return parts[1]
	}
	return ""
}
