//go:build windows

package secrets

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/danieljoos/wincred"
)

type credentialRecord interface {
	Blob() []byte
	SetBlob([]byte)
	Write() error
	Delete() error
}

type nativeCredential struct {
	value *wincred.GenericCredential
}

func (c *nativeCredential) Blob() []byte         { return c.value.CredentialBlob }
func (c *nativeCredential) SetBlob(value []byte) { c.value.CredentialBlob = value }
func (c *nativeCredential) Write() error         { return c.value.Write() }
func (c *nativeCredential) Delete() error        { return c.value.Delete() }

type credentialBackend interface {
	Get(name string) (credentialRecord, error)
	New(name string) credentialRecord
}

type nativeBackend struct {
}

func (nativeBackend) Get(name string) (credentialRecord, error) {
	credential, err := wincred.GetGenericCredential(name)
	if err != nil {
		return nil, err
	}

	return &nativeCredential{value: credential}, nil
}

func (nativeBackend) New(name string) credentialRecord {
	return &nativeCredential{value: wincred.NewGenericCredential(name)}
}

type WindowsStore struct {
	backend credentialBackend
}

func NewWindowsStore() *WindowsStore {
	return &WindowsStore{backend: nativeBackend{}}
}

func (s *WindowsStore) Get(ctx context.Context, name string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	credential, err := s.backend.Get(name)
	if err != nil {
		if errors.Is(err, wincred.ErrElementNotFound) {
			return nil, fmt.Errorf("get credential %q: %w", name, ErrNotFound)
		}

		return nil, fmt.Errorf("get credential %q: %w", name, err)
	}

	return bytes.Clone(credential.Blob()), nil
}

func (s *WindowsStore) Set(ctx context.Context, name string, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	credential := s.backend.New(name)
	credential.SetBlob(bytes.Clone(value))
	if err := credential.Write(); err != nil {
		return fmt.Errorf("set credential %q: %w", name, err)
	}
	return nil
}

func (s *WindowsStore) Delete(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	credential, err := s.backend.Get(name)
	if errors.Is(err, wincred.ErrElementNotFound) {
		return fmt.Errorf("delete credential %q: %w", name, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("get credential %q for deletion: %w", name, err)
	}
	if err := credential.Delete(); err != nil {
		return fmt.Errorf("delete credential %q: %w", name, err)
	}
	return nil
}

var _ Store = (*WindowsStore)(nil)
