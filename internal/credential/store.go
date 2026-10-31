package credential

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"github.com/r0jjames/bam-cli/internal/atomicfile"
	"github.com/r0jjames/bam-cli/internal/errs"
	"github.com/zalando/go-keyring"
	"gopkg.in/yaml.v3"
)

// Service is the keychain service name; the account is the server origin.
const Service = "bam"

// Keyring is the subset of an OS keychain bam uses.
type Keyring interface {
	Get(service, user string) (string, error)
	Set(service, user, password string) error
	Delete(service, user string) error
}

// SystemKeyring uses the OS keychain: macOS Keychain, Linux Secret Service,
// Windows Credential Manager.
type SystemKeyring struct{}

func (SystemKeyring) Get(s, u string) (string, error) { return keyring.Get(s, u) }
func (SystemKeyring) Set(s, u, p string) error        { return keyring.Set(s, u, p) }
func (SystemKeyring) Delete(s, u string) error        { return keyring.Delete(s, u) }

// Source says where a token was found or stored.
type Source string

const (
	SourceEnv      Source = "env"
	SourceKeychain Source = "keychain"
	SourceFile     Source = "file"
)

// Store looks tokens up in order: the server's auth_env variable, the
// keychain, then the 0600 credentials file.
type Store struct {
	Keyring  Keyring
	FilePath string
	Getenv   func(string) string
}

// Lookup returns the token for origin. alias is used only in messages.
func (s *Store) Lookup(alias, origin, authEnv string) (string, Source, error) {
	if authEnv != "" {
		if v := s.Getenv(authEnv); v != "" {
			return v, SourceEnv, nil
		}
	}
	if v, err := s.Keyring.Get(Service, origin); err == nil && v != "" {
		return v, SourceKeychain, nil
	}
	tokens, err := s.readFile()
	if err != nil {
		return "", "", err
	}
	if v := tokens[origin]; v != "" {
		return v, SourceFile, nil
	}
	e := errs.Authf("no token for server %q (%s)", alias, origin).WithTry("bam login " + alias)
	if authEnv != "" {
		e = e.WithWhy(fmt.Sprintf("%s is not set and nothing is stored", authEnv))
	}
	return "", "", e
}

// Has reports whether a token exists for origin, without failing.
func (s *Store) Has(origin, authEnv string) bool {
	_, _, err := s.Lookup("", origin, authEnv)
	return err == nil
}

// Save stores token in the keychain, or in the credentials file when no
// keychain is available.
func (s *Store) Save(origin, token string) (Source, error) {
	if err := s.Keyring.Set(Service, origin, token); err == nil {
		return SourceKeychain, nil
	}
	tokens, err := s.readFile()
	if err != nil {
		return "", err
	}
	tokens[origin] = token
	if err := s.writeFile(tokens); err != nil {
		return "", err
	}
	return SourceFile, nil
}

// Delete removes the token for origin from the keychain and the file.
func (s *Store) Delete(origin string) (bool, error) {
	deleted := s.Keyring.Delete(Service, origin) == nil
	tokens, err := s.readFile()
	if err != nil {
		return deleted, err
	}
	if _, ok := tokens[origin]; ok {
		delete(tokens, origin)
		if err := s.writeFile(tokens); err != nil {
			return deleted, err
		}
		deleted = true
	}
	return deleted, nil
}

func (s *Store) readFile() (map[string]string, error) {
	tokens := map[string]string{}
	if s.FilePath == "" {
		return tokens, nil
	}
	st, err := os.Stat(s.FilePath)
	if errors.Is(err, fs.ErrNotExist) {
		return tokens, nil
	}
	if err != nil {
		return nil, errs.Configf("cannot read %s", s.FilePath).Wrap(err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm()&0o077 != 0 {
		return nil, errs.Configf("%s is readable by other users (mode %04o)", s.FilePath, st.Mode().Perm()).
			WithWhy("the credentials file must be 0600").
			WithTry("chmod 600 " + s.FilePath)
	}
	data, err := os.ReadFile(s.FilePath)
	if err != nil {
		return nil, errs.Configf("cannot read %s", s.FilePath).Wrap(err)
	}
	if err := yaml.Unmarshal(data, &tokens); err != nil {
		return nil, errs.Configf("invalid %s", s.FilePath).WithWhy(err.Error())
	}
	return tokens, nil
}

func (s *Store) writeFile(tokens map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(s.FilePath), 0o700); err != nil {
		return errs.Configf("cannot create %s", filepath.Dir(s.FilePath)).Wrap(err)
	}
	data, err := yaml.Marshal(tokens)
	if err != nil {
		return err
	}
	if err := atomicfile.Write(s.FilePath, data, 0o600); err != nil {
		return errs.Configf("cannot write %s", s.FilePath).Wrap(err)
	}
	return nil
}
