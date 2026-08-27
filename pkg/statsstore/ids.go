package statsstore

import "github.com/google/uuid"

func newEventID() string { return uuid.NewString() }
