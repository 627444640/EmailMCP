//go:build windows

package credentials

import (
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	credTypeGeneric         = 1
	credPersistLocalMachine = 2
)

var (
	advapi32        = windows.NewLazySystemDLL("advapi32.dll")
	procCredWriteW  = advapi32.NewProc("CredWriteW")
	procCredReadW   = advapi32.NewProc("CredReadW")
	procCredFree    = advapi32.NewProc("CredFree")
	procCredDeleteW = advapi32.NewProc("CredDeleteW")
)

type nativeCredential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

// Set writes a secret to Windows Credential Manager.
func (Store) Set(kind, value string) error {
	targetName, err := windows.UTF16PtrFromString(target(kind))
	if err != nil {
		return err
	}
	userName, err := windows.UTF16PtrFromString("default")
	if err != nil {
		return err
	}
	blob := []byte(value)
	cred := nativeCredential{
		Type:               credTypeGeneric,
		TargetName:         targetName,
		CredentialBlobSize: uint32(len(blob)),
		Persist:            credPersistLocalMachine,
		UserName:           userName,
	}
	if len(blob) > 0 {
		cred.CredentialBlob = &blob[0]
	}
	ret, _, callErr := procCredWriteW.Call(uintptr(unsafe.Pointer(&cred)), 0)
	runtime.KeepAlive(blob)
	if ret == 0 {
		return fmt.Errorf("CredWrite failed: %w", callErr)
	}
	return nil
}

// Get reads a secret from Windows Credential Manager.
func (Store) Get(kind string) (string, error) {
	targetName, err := windows.UTF16PtrFromString(target(kind))
	if err != nil {
		return "", err
	}
	var cred *nativeCredential
	ret, _, callErr := procCredReadW.Call(
		uintptr(unsafe.Pointer(targetName)),
		credTypeGeneric,
		0,
		uintptr(unsafe.Pointer(&cred)),
	)
	if ret == 0 {
		if errno, ok := callErr.(windows.Errno); ok && errno == windows.ERROR_NOT_FOUND {
			return "", nil
		}
		return "", fmt.Errorf("CredRead failed: %w", callErr)
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(cred)))
	if cred.CredentialBlobSize == 0 || cred.CredentialBlob == nil {
		return "", nil
	}
	data := unsafe.Slice(cred.CredentialBlob, cred.CredentialBlobSize)
	return string(append([]byte(nil), data...)), nil
}

// Delete removes a secret from Windows Credential Manager.
func (Store) Delete(kind string) error {
	targetName, err := windows.UTF16PtrFromString(target(kind))
	if err != nil {
		return err
	}
	ret, _, callErr := procCredDeleteW.Call(uintptr(unsafe.Pointer(targetName)), credTypeGeneric, 0)
	if ret == 0 {
		if errno, ok := callErr.(windows.Errno); ok && errno == windows.ERROR_NOT_FOUND {
			return nil
		}
		return fmt.Errorf("CredDelete failed: %w", callErr)
	}
	return nil
}
