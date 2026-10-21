package auth

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

var ErrUnauthorized = errors.New("unauthorized")

type Authenticator interface {
	Authenticate(context.Context, string) (string, error)
}

type JupyterHub struct {
	url    string
	client *http.Client
	mu     sync.Mutex
	cache  map[[32]byte]cachedIdentity
}

type cachedIdentity struct {
	user      string
	expiresAt time.Time
}

// NewJupyterHub implements the new jupyter hub operation for this package.
func NewJupyterHub(url string, timeout time.Duration) *JupyterHub {
	return &JupyterHub{url: url, client: &http.Client{Timeout: timeout}, cache: make(map[[32]byte]cachedIdentity)}
}

// Authenticate implements the authenticate operation for this package.
func (j *JupyterHub) Authenticate(ctx context.Context, authorization string) (string, error) {
	if !strings.HasPrefix(authorization, "Bearer ") || len(strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer "))) < 16 {
		return "", ErrUnauthorized
	}
	cacheKey := sha256.Sum256([]byte(authorization))
	j.mu.Lock()
	cached, ok := j.cache[cacheKey]
	j.mu.Unlock()
	if ok && time.Now().Before(cached.expiresAt) {
		return cached.user, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, j.url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", authorization)
	resp, err := j.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("validate JupyterHub token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", ErrUnauthorized
	}
	var user struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil || user.Name == "" {
		return "", ErrUnauthorized
	}
	j.mu.Lock()
	now := time.Now()
	if len(j.cache) >= 1024 {
		for key, value := range j.cache {
			if !now.Before(value.expiresAt) {
				delete(j.cache, key)
			}
		}
	}
	if len(j.cache) < 1024 {
		j.cache[cacheKey] = cachedIdentity{user: user.Name, expiresAt: now.Add(30 * time.Second)}
	}
	j.mu.Unlock()
	return user.Name, nil
}
