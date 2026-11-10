package credential

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/rojcarranza/bam/internal/errs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

type memKeyring struct {
	data map[string]string
	fail error // returned by every call when set, like a missing Secret Service
}

func (m *memKeyring) Get(service, user string) (string, error) {
	if m.fail != nil {
		return "", m.fail
	}
	v, ok := m.data[service+"|"+user]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return v, nil
}

func (m *memKeyring) Set(service, user, pw string) error {
	if m.fail != nil {
		return m.fail
	}
	if m.data == nil {
		m.data = map[string]string{}
	}
	m.data[service+"|"+user] = pw
	return nil
}

func (m *memKeyring) Delete(service, user string) error {
	if m.fail != nil {
		return m.fail
	}
	if _, ok := m.data[service+"|"+user]; !ok {
		return keyring.ErrNotFound
	}
	delete(m.data, service+"|"+user)
	return nil
}

const workOrigin = "https://bamboo.example.com"

func newStore(t *testing.T, kr *memKeyring, env map[string]string) *Store {
	return &Store{
		Keyring:  kr,
		FilePath: filepath.Join(t.TempDir(), "credentials.yaml"),
		Getenv:   func(k string) string { return env[k] },
	}
}

func TestLookupOrderEnvKeychainFile(t *testing.T) {
	kr := &memKeyring{}
	s := newStore(t, kr, map[string]string{"BAM_WORK_TOKEN": "from-env"})
	require.NoError(t, os.WriteFile(s.FilePath, []byte(workOrigin+": from-file\n"), 0o600))

	tok, src, err := s.Lookup("work", workOrigin, "")
	require.NoError(t, err)
	assert.Equal(t, "from-file", tok)
	assert.Equal(t, SourceFile, src)

	require.NoError(t, kr.Set(Service, workOrigin, "from-keychain"))
	tok, src, _ = s.Lookup("work", workOrigin, "")
	assert.Equal(t, "from-keychain", tok)
	assert.Equal(t, SourceKeychain, src)

	tok, src, _ = s.Lookup("work", workOrigin, "BAM_WORK_TOKEN")
	assert.Equal(t, "from-env", tok)
	assert.Equal(t, SourceEnv, src)
}

func TestLookupMissingIsAuthError(t *testing.T) {
	s := newStore(t, &memKeyring{}, nil)
	_, _, err := s.Lookup("work", workOrigin, "")
	require.Error(t, err)
	assert.Equal(t, errs.KindAuth, errs.KindOf(err))
	var e *errs.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, "bam login work", e.Try)
}

func TestTokensNeverCrossOrigins(t *testing.T) {
	s := newStore(t, &memKeyring{}, nil)
	_, err := s.Save(workOrigin, "work-token")
	require.NoError(t, err)
	_, _, err = s.Lookup("work", "https://evil.example.com", "")
	assert.Error(t, err, "same alias on another host must not find the token")
	assert.False(t, s.Has("https://evil.example.com", ""))
	assert.True(t, s.Has(workOrigin, ""))
}

func TestLooseFilePermissionsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions only")
	}
	s := newStore(t, &memKeyring{}, nil)
	require.NoError(t, os.WriteFile(s.FilePath, []byte(workOrigin+": tok\n"), 0o644))
	_, _, err := s.Lookup("work", workOrigin, "")
	require.Error(t, err)
	assert.Equal(t, errs.KindConfig, errs.KindOf(err))
	assert.Contains(t, err.Error(), "0600")
}

func TestSaveFallsBackToFileWithoutKeychain(t *testing.T) {
	s := newStore(t, &memKeyring{fail: errors.New("no Secret Service")}, nil)
	src, err := s.Save(workOrigin, "tok")
	require.NoError(t, err)
	assert.Equal(t, SourceFile, src)
	if runtime.GOOS != "windows" {
		st, err := os.Stat(s.FilePath)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), st.Mode().Perm())
	}
	tok, src, err := s.Lookup("work", workOrigin, "")
	require.NoError(t, err)
	assert.Equal(t, "tok", tok)
	assert.Equal(t, SourceFile, src)
}

func TestDeleteRemovesFromKeychainAndFile(t *testing.T) {
	kr := &memKeyring{}
	s := newStore(t, kr, nil)
	require.NoError(t, kr.Set(Service, workOrigin, "a"))
	require.NoError(t, os.WriteFile(s.FilePath, []byte(workOrigin+": b\nhttp://other.example: c\n"), 0o600))

	deleted, err := s.Delete(workOrigin)
	require.NoError(t, err)
	assert.True(t, deleted)
	assert.False(t, s.Has(workOrigin, ""))
	assert.True(t, s.Has("http://other.example", ""))

	deleted, err = s.Delete(workOrigin)
	require.NoError(t, err)
	assert.False(t, deleted)
}
