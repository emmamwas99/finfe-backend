package httpapi

import (
	"encoding/json"
	"time"
)

type vault struct {
	Version     int64           `json:"version"`
	KDF         json.RawMessage `json:"kdf"`
	WrappedKey  json.RawMessage `json:"wrappedKey"`
	RecoveryKey json.RawMessage `json:"recoveryKey"`
	Data        json.RawMessage `json:"data"`
	UpdatedAt   time.Time       `json:"updatedAt"`
}

func validJSON(x json.RawMessage) bool { return len(x) > 0 && json.Valid(x) && string(x) != "null" }
