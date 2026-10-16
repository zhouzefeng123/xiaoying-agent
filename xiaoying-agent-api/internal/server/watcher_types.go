package server

import (
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

type Watcher struct {
	watcher      *fsnotify.Watcher
	server       *Server
	debounceMu   sync.Mutex
	timer        *time.Timer
	pendingPaths map[string]bool
}

func NewWatcher(s *Server) (*Watcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &Watcher{
		watcher:      w,
		server:       s,
		pendingPaths: make(map[string]bool),
	}, nil
}