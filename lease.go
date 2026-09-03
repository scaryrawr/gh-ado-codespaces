package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
)

type leaseOwner struct {
	Codespace        string    `json:"codespace"`
	WorkingDirectory string    `json:"working_directory"`
	AcquiredAt       time.Time `json:"acquired_at"`
}

type leaseConflictError struct {
	Owner *leaseOwner
}

func (e *leaseConflictError) Error() string {
	return "the codespace working directory is already leased"
}

type agentLease struct {
	lock         *flock.Flock
	metadataPath string
	once         sync.Once
	err          error
}

func (l *agentLease) Release() error {
	l.once.Do(func() {
		removeErr := os.Remove(l.metadataPath)
		if errors.Is(removeErr, os.ErrNotExist) {
			removeErr = nil
		}
		l.err = errors.Join(removeErr, l.lock.Unlock())
	})
	return l.err
}

type leaseManager struct {
	directory string
	now       func() time.Time
}

func newLeaseManager(directory string) *leaseManager {
	return &leaseManager{directory: directory, now: time.Now}
}

func defaultLeaseManager() *leaseManager {
	cacheDirectory, err := os.UserCacheDir()
	if err != nil {
		cacheDirectory = os.TempDir()
	}
	return newLeaseManager(filepath.Join(cacheDirectory, "gh-ado-codespaces", "agent-leases"))
}

func (m *leaseManager) Acquire(codespace, workingDirectory string) (*agentLease, error) {
	key := canonicalLeaseKey(codespace, workingDirectory)
	if err := os.MkdirAll(m.directory, 0700); err != nil {
		return nil, fmt.Errorf("create lease directory: %w", err)
	}

	lockPath := filepath.Join(m.directory, key+".lock")
	metadataPath := filepath.Join(m.directory, key+".owner.json")
	lock := flock.New(lockPath)
	locked, err := lock.TryLock()
	if err != nil {
		return nil, fmt.Errorf("acquire lease: %w", err)
	}
	if !locked {
		return nil, &leaseConflictError{Owner: readLeaseOwner(metadataPath)}
	}

	owner := leaseOwner{
		Codespace:        redactSecrets(strings.TrimSpace(codespace)),
		WorkingDirectory: redactSecrets(canonicalWorkingDirectory(workingDirectory)),
		AcquiredAt:       m.now().UTC(),
	}
	if err := writeLeaseOwner(metadataPath, owner); err != nil {
		_ = lock.Unlock()
		return nil, err
	}
	return &agentLease{lock: lock, metadataPath: metadataPath}, nil
}

func canonicalLeaseKey(codespace, workingDirectory string) string {
	input := strings.ToLower(strings.TrimSpace(codespace)) + "\x00" + canonicalWorkingDirectory(workingDirectory)
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])
}

func canonicalWorkingDirectory(workingDirectory string) string {
	directory := strings.TrimSpace(workingDirectory)
	if directory == "" {
		return "."
	}
	return path.Clean(directory)
}

func readLeaseOwner(metadataPath string) *leaseOwner {
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		return nil
	}
	var owner leaseOwner
	if err := json.Unmarshal(data, &owner); err != nil {
		return nil
	}
	return &owner
}

func writeLeaseOwner(metadataPath string, owner leaseOwner) error {
	data, err := json.Marshal(owner)
	if err != nil {
		return fmt.Errorf("marshal lease owner: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(metadataPath), filepath.Base(metadataPath)+".")
	if err != nil {
		return fmt.Errorf("create lease metadata: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0600); err != nil {
		temporary.Close()
		return fmt.Errorf("set lease metadata permissions: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write lease metadata: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close lease metadata: %w", err)
	}
	if err := os.Remove(metadataPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("replace lease metadata: %w", err)
	}
	if err := os.Rename(temporaryPath, metadataPath); err != nil {
		return fmt.Errorf("publish lease metadata: %w", err)
	}
	return nil
}
