package stats

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/thenickygee/mirage/internal/config"
)

type AgentStats struct {
	Runs         int
	InputTokens  int64
	OutputTokens int64
	CacheRead    int64
	Cost         float64
	LastUsed     time.Time
}

type messageTokens struct {
	Input     int64 `json:"input"`
	Output    int64 `json:"output"`
	Reasoning int64 `json:"reasoning"`
	Cache     struct {
		Read  int64 `json:"read"`
		Write int64 `json:"write"`
	} `json:"cache"`
}

type messageRecord struct {
	Agent   string        `json:"agent"`
	ModelID string        `json:"modelID"`
	Tokens  messageTokens `json:"tokens"`
	Cost    float64       `json:"cost"`
	Time    struct {
		Completed int64 `json:"completed"`
	} `json:"time"`
}

func LoadAll() (map[string]*AgentStats, error) {
	msgDir, err := config.MessagesDir()
	if err != nil {
		return nil, err
	}

	result := make(map[string]*AgentStats)

	sessions, err := os.ReadDir(msgDir)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return nil, err
	}

	for _, session := range sessions {
		if !session.IsDir() {
			continue
		}
		sessionDir := filepath.Join(msgDir, session.Name())
		messages, err := os.ReadDir(sessionDir)
		if err != nil {
			continue
		}
		for _, msg := range messages {
			if msg.IsDir() || filepath.Ext(msg.Name()) != ".json" {
				continue
			}
			data, err := os.ReadFile(filepath.Join(sessionDir, msg.Name()))
			if err != nil {
				continue
			}
			var rec messageRecord
			if err := json.Unmarshal(data, &rec); err != nil {
				continue
			}
			if rec.Agent == "" || rec.ModelID == "" {
				continue
			}
			s, ok := result[rec.Agent]
			if !ok {
				s = &AgentStats{}
				result[rec.Agent] = s
			}
			s.Runs++
			s.InputTokens += rec.Tokens.Input
			s.OutputTokens += rec.Tokens.Output
			s.CacheRead += rec.Tokens.Cache.Read
			s.Cost += rec.Cost
			if rec.Time.Completed > 0 {
				t := time.UnixMilli(rec.Time.Completed)
				if t.After(s.LastUsed) {
					s.LastUsed = t
				}
			}
		}
	}

	return result, nil
}
