package httpapi

import (
	"encoding/json"
	"testing"
)

func TestValidateSnapshotFieldsAcceptsFamilyEmoji(t *testing.T) {
	var snapshot financeSnapshot
	err := json.Unmarshal([]byte(`{"categories":[{"id":"3623de96-dd55-46d7-8326-68fb9b431641","name":"Family Support","emoji":"👨‍👩‍👧","kind":"need","budget":0}]}`), &snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateSnapshotFields(snapshot); err != nil {
		t.Fatalf("browser default category rejected: %v", err)
	}
}
