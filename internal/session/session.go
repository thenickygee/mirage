package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/thenickygee/mirage/internal/config"
)

type Permission struct {
	Permission string `json:"permission"`
	Action     string `json:"action"`
	Pattern    string `json:"pattern"`
}

type Summary struct {
	Additions int `json:"additions"`
	Deletions int `json:"deletions"`
	Files     int `json:"files"`
}

type Session struct {
	ID          string       `json:"id"`
	Version     string       `json:"version"`
	ProjectID   string       `json:"projectID"`
	Directory   string       `json:"directory"`
	ParentID    string       `json:"parentID"`
	Title       string       `json:"title"`
	Permissions []Permission `json:"permission"`
	Time        struct {
		Created int64 `json:"created"`
		Updated int64 `json:"updated"`
	} `json:"time"`
	Summary Summary `json:"summary"`

	// Derived fields (not from JSON)
	Children []*Session
	Depth    int
}

func (s *Session) CreatedAt() time.Time {
	if s.Time.Created == 0 {
		return time.Time{}
	}
	return time.UnixMilli(s.Time.Created)
}

func (s *Session) UpdatedAt() time.Time {
	if s.Time.Updated == 0 {
		return time.Time{}
	}
	return time.UnixMilli(s.Time.Updated)
}

func (s *Session) ShortID() string {
	if len(s.ID) > 12 {
		return s.ID[:12] + "…"
	}
	return s.ID
}

func (s *Session) DisplayTitle() string {
	if s.Title != "" {
		return s.Title
	}
	return s.ShortID()
}

// LoadAll reads all session JSON files from the platform-appropriate opencode sessions
// directory and returns a flat list sorted by updated-at descending, plus a tree-ordered
// list for display (parents followed by their indented children).
func LoadAll() ([]*Session, error) {
	sesDir, err := config.SessionsDir()
	if err != nil {
		return nil, err
	}

	projectDirs, err := os.ReadDir(sesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	byID := make(map[string]*Session)

	for _, pd := range projectDirs {
		if !pd.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(sesDir, pd.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(sesDir, pd.Name(), f.Name()))
			if err != nil {
				continue
			}
			var s Session
			if err := json.Unmarshal(data, &s); err != nil {
				continue
			}
			if s.ID == "" {
				continue
			}
			byID[s.ID] = &s
		}
	}

	// Build parent-child relationships
	var roots []*Session
	for _, s := range byID {
		if s.ParentID == "" {
			roots = append(roots, s)
		} else if parent, ok := byID[s.ParentID]; ok {
			parent.Children = append(parent.Children, s)
		} else {
			// Parent not found — treat as root
			roots = append(roots, s)
		}
	}

	// Sort children by updated-at descending within each parent
	var sortSessions func([]*Session)
	sortSessions = func(list []*Session) {
		sort.Slice(list, func(i, j int) bool {
			return list[i].UpdatedAt().After(list[j].UpdatedAt())
		})
		for _, s := range list {
			sortSessions(s.Children)
		}
	}
	sortSessions(roots)

	// Flatten tree into display order (parent then children, depth-first)
	var flat []*Session
	var walk func(list []*Session, depth int)
	walk = func(list []*Session, depth int) {
		for _, s := range list {
			s.Depth = depth
			flat = append(flat, s)
			walk(s.Children, depth+1)
		}
	}
	walk(roots, 0)

	return flat, nil
}
