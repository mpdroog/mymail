package main

import (
	"encoding/json"
	"os"
	"sync"
)

type UserStore struct {
	mu    sync.RWMutex
	users map[string]string // username -> password
	path  string
}

func NewUserStore(path string) (*UserStore, error) {
	us := &UserStore{
		users: make(map[string]string),
		path:  path,
	}
	if err := us.Load(); err != nil {
		return nil, err
	}
	return us, nil
}

func (us *UserStore) Load() error {
	us.mu.Lock()
	defer us.mu.Unlock()

	f, err := os.Open(us.path)
	if err != nil {
		if os.IsNotExist(err) {
			us.users = make(map[string]string)
			return nil
		}
		return err
	}
	defer f.Close()

	users := make(map[string]string)
	if err := json.NewDecoder(f).Decode(&users); err != nil {
		return err
	}
	us.users = users
	return nil
}

func (us *UserStore) Validate(username, password string) bool {
	us.mu.RLock()
	defer us.mu.RUnlock()

	storedPass, exists := us.users[username]
	if !exists {
		return false
	}
	return storedPass == password
}

func (us *UserStore) Reload() error {
	return us.Load()
}
