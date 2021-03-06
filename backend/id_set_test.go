package backend_test

import (
	"crypto/rand"
	"io"
	"testing"

	"github.com/restic/restic/backend"
)

func randomID() []byte {
	buf := make([]byte, backend.IDSize)
	_, err := io.ReadFull(rand.Reader, buf)
	if err != nil {
		panic(err)
	}
	return buf
}

func TestSet(t *testing.T) {
	s := backend.NewIDSet()

	testID := randomID()
	err := s.Find(testID)
	assert(t, err != nil, "found test ID in IDSet before insertion")

	for i := 0; i < 238; i++ {
		s.Insert(randomID())
	}

	s.Insert(testID)
	ok(t, s.Find(testID))

	for i := 0; i < 80; i++ {
		s.Insert(randomID())
	}

	s.Insert(testID)
	ok(t, s.Find(testID))
}
