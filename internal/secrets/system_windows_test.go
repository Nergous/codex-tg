//go:build windows

package secrets

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/danieljoos/wincred"
)

type fakeCredential struct {
	blob        []byte
	writeErr    error
	deleteErr   error
	writeCalls  int
	deleteCalls int
}

func (c *fakeCredential) Blob() []byte         { return c.blob }
func (c *fakeCredential) SetBlob(value []byte) { c.blob = value }
func (c *fakeCredential) Write() error {
	c.writeCalls++
	return c.writeErr
}
func (c *fakeCredential) Delete() error {
	c.deleteCalls++
	return c.deleteErr
}

type fakeCredentialBackend struct {
	credential credentialRecord
	getErr     error
	getCalls   int
	newCalls   int
	getName    string
	newName    string
}

func (b *fakeCredentialBackend) Get(name string) (credentialRecord, error) {
	b.getCalls++
	b.getName = name
	if b.getErr != nil {
		return nil, b.getErr
	}
	return b.credential, nil
}

func (b *fakeCredentialBackend) New(name string) credentialRecord {
	b.newCalls++
	b.newName = name
	return b.credential
}

func TestNewWindowsStore(t *testing.T) {
	t.Parallel()

	store := NewWindowsStore()
	if store == nil {
		t.Fatal("NewWindowsStore() = nil")
	}
	if _, ok := store.backend.(nativeBackend); !ok {
		t.Fatalf("backend type = %T, want nativeBackend", store.backend)
	}
}

func TestNativeCredentialBlob(t *testing.T) {
	t.Parallel()

	tests := map[string][]byte{
		"nil":       nil,
		"empty":     {},
		"non-empty": []byte("secret"),
	}

	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			credential := &nativeCredential{
				value: wincred.NewGenericCredential("codex-tg/test-not-written"),
			}
			credential.SetBlob(value)

			if got := credential.Blob(); !bytes.Equal(got, value) {
				t.Fatalf("Blob() = %q, want %q", got, value)
			}
		})
	}
}

func TestNativeBackendNew(t *testing.T) {
	t.Parallel()

	const name = "codex-tg/test-not-written"
	record := (nativeBackend{}).New(name)
	credential, ok := record.(*nativeCredential)
	if !ok {
		t.Fatalf("New() type = %T, want *nativeCredential", record)
	}
	if credential.value.TargetName != name {
		t.Fatalf("TargetName = %q, want %q", credential.value.TargetName, name)
	}
}

func TestWindowsStoreGet(t *testing.T) {
	t.Parallel()

	errBackend := errors.New("backend failed")
	tests := map[string]struct {
		ctx          func() context.Context
		credential   *fakeCredential
		backendErr   error
		want         []byte
		wantErr      error
		wantGetCalls int
	}{
		"success": {
			ctx:          context.Background,
			credential:   &fakeCredential{blob: []byte("secret")},
			want:         []byte("secret"),
			wantGetCalls: 1,
		},
		"not found": {
			ctx:          context.Background,
			backendErr:   wincred.ErrElementNotFound,
			wantErr:      ErrNotFound,
			wantGetCalls: 1,
		},
		"backend error": {
			ctx:          context.Background,
			backendErr:   errBackend,
			wantErr:      errBackend,
			wantGetCalls: 1,
		},
		"canceled context": {
			ctx:          canceledContext,
			credential:   &fakeCredential{blob: []byte("secret")},
			wantErr:      context.Canceled,
			wantGetCalls: 0,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			backend := &fakeCredentialBackend{
				credential: tc.credential,
				getErr:     tc.backendErr,
			}
			store := &WindowsStore{backend: backend}

			got, err := store.Get(tc.ctx(), TelegramBotToken)
			assertErrorIs(t, err, tc.wantErr)
			if !bytes.Equal(got, tc.want) {
				t.Fatalf("Get() = %q, want %q", got, tc.want)
			}
			if backend.getCalls != tc.wantGetCalls {
				t.Fatalf("backend.Get() calls = %d, want %d", backend.getCalls, tc.wantGetCalls)
			}
			if backend.getCalls > 0 && backend.getName != TelegramBotToken {
				t.Fatalf("backend.Get() name = %q, want %q", backend.getName, TelegramBotToken)
			}

			if err == nil && len(got) > 0 {
				got[0] ^= 0xff
				if bytes.Equal(got, tc.credential.blob) {
					t.Fatal("Get() returned backend-owned byte slice")
				}
			}
		})
	}
}

func TestWindowsStoreSet(t *testing.T) {
	t.Parallel()

	errWrite := errors.New("write failed")
	tests := map[string]struct {
		ctx            func() context.Context
		value          []byte
		writeErr       error
		wantErr        error
		wantNewCalls   int
		wantWriteCalls int
	}{
		"success": {
			ctx:            context.Background,
			value:          []byte("secret"),
			wantNewCalls:   1,
			wantWriteCalls: 1,
		},
		"empty value": {
			ctx:            context.Background,
			value:          []byte{},
			wantNewCalls:   1,
			wantWriteCalls: 1,
		},
		"write error": {
			ctx:            context.Background,
			value:          []byte("never-log-me"),
			writeErr:       errWrite,
			wantErr:        errWrite,
			wantNewCalls:   1,
			wantWriteCalls: 1,
		},
		"canceled context": {
			ctx:            canceledContext,
			value:          []byte("secret"),
			wantErr:        context.Canceled,
			wantNewCalls:   0,
			wantWriteCalls: 0,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			credential := &fakeCredential{writeErr: tc.writeErr}
			backend := &fakeCredentialBackend{credential: credential}
			store := &WindowsStore{backend: backend}
			input := bytes.Clone(tc.value)

			err := store.Set(tc.ctx(), TelegramBotToken, input)
			assertErrorIs(t, err, tc.wantErr)
			if backend.newCalls != tc.wantNewCalls {
				t.Fatalf("backend.New() calls = %d, want %d", backend.newCalls, tc.wantNewCalls)
			}
			if credential.writeCalls != tc.wantWriteCalls {
				t.Fatalf("credential.Write() calls = %d, want %d", credential.writeCalls, tc.wantWriteCalls)
			}
			if backend.newCalls > 0 {
				if backend.newName != TelegramBotToken {
					t.Fatalf("backend.New() name = %q, want %q", backend.newName, TelegramBotToken)
				}
				if !bytes.Equal(credential.blob, tc.value) {
					t.Fatalf("stored value = %q, want %q", credential.blob, tc.value)
				}
				if len(input) > 0 {
					input[0] ^= 0xff
					if bytes.Equal(input, credential.blob) {
						t.Fatal("Set() retained caller-owned byte slice")
					}
				}
			}
			if err != nil && strings.Contains(err.Error(), "never-log-me") {
				t.Fatalf("Set() error exposes secret: %v", err)
			}
		})
	}
}

func TestWindowsStoreDelete(t *testing.T) {
	t.Parallel()

	errBackend := errors.New("backend failed")
	errDelete := errors.New("delete failed")
	tests := map[string]struct {
		ctx             func() context.Context
		backendErr      error
		deleteErr       error
		wantErr         error
		wantGetCalls    int
		wantDeleteCalls int
	}{
		"success": {
			ctx:             context.Background,
			wantGetCalls:    1,
			wantDeleteCalls: 1,
		},
		"not found": {
			ctx:          context.Background,
			backendErr:   wincred.ErrElementNotFound,
			wantErr:      ErrNotFound,
			wantGetCalls: 1,
		},
		"backend error": {
			ctx:          context.Background,
			backendErr:   errBackend,
			wantErr:      errBackend,
			wantGetCalls: 1,
		},
		"delete error": {
			ctx:             context.Background,
			deleteErr:       errDelete,
			wantErr:         errDelete,
			wantGetCalls:    1,
			wantDeleteCalls: 1,
		},
		"canceled context": {
			ctx:          canceledContext,
			wantErr:      context.Canceled,
			wantGetCalls: 0,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			credential := &fakeCredential{deleteErr: tc.deleteErr}
			backend := &fakeCredentialBackend{
				credential: credential,
				getErr:     tc.backendErr,
			}
			store := &WindowsStore{backend: backend}

			err := store.Delete(tc.ctx(), TelegramBotToken)
			assertErrorIs(t, err, tc.wantErr)
			if backend.getCalls != tc.wantGetCalls {
				t.Fatalf("backend.Get() calls = %d, want %d", backend.getCalls, tc.wantGetCalls)
			}
			if credential.deleteCalls != tc.wantDeleteCalls {
				t.Fatalf("credential.Delete() calls = %d, want %d", credential.deleteCalls, tc.wantDeleteCalls)
			}
			if backend.getCalls > 0 && backend.getName != TelegramBotToken {
				t.Fatalf("backend.Get() name = %q, want %q", backend.getName, TelegramBotToken)
			}
		})
	}
}

func canceledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func assertErrorIs(t *testing.T, got, want error) {
	t.Helper()
	if want == nil {
		if got != nil {
			t.Fatalf("error = %v, want nil", got)
		}
		return
	}
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want errors.Is(_, %v)", got, want)
	}
}
