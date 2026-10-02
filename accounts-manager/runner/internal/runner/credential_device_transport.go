package runner

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func deviceCipher(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errDeviceLogin
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errDeviceLogin
	}
	return cipher.NewGCM(block)
}

func sealDeviceCredential(key []byte, incoming *coreauth.Auth) (string, error) {
	auth, err := normalizedVaultAuth(incoming)
	if err != nil || auth.Provider != "codex" {
		return "", errDeviceLogin
	}
	aead, err := deviceCipher(key)
	if err != nil {
		return "", err
	}
	plain, err := json.Marshal(auth)
	if err != nil || len(plain) > 1<<20 {
		return "", errDeviceLogin
	}
	defer clear(plain)
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", errDeviceLogin
	}
	sealed := aead.Seal(nonce, nonce, plain, []byte(deviceResultPrefix))
	return base64.RawStdEncoding.EncodeToString(sealed), nil
}

func openDeviceCredential(key []byte, encoded string) (*coreauth.Auth, error) {
	if len(encoded) > deviceFrameLimit {
		return nil, errDeviceLogin
	}
	aead, err := deviceCipher(key)
	if err != nil {
		return nil, err
	}
	sealed, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil || len(sealed) < aead.NonceSize()+aead.Overhead() {
		return nil, errDeviceLogin
	}
	plain, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], []byte(deviceResultPrefix))
	if err != nil {
		return nil, errDeviceLogin
	}
	defer clear(plain)
	var auth coreauth.Auth
	if len(plain) > 1<<20 || json.Unmarshal(plain, &auth) != nil || auth.Provider != "codex" {
		return nil, errDeviceLogin
	}
	return &auth, nil
}
