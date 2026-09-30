package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sync"
	"time"
)

func ellipticCurve(crv string) (elliptic.Curve, error) {
	switch crv {
	case "P-256":
		return elliptic.P256(), nil
	case "P-384":
		return elliptic.P384(), nil
	case "P-521":
		return elliptic.P521(), nil
	default:
		return nil, fmt.Errorf("unsupported EC curve %q", crv)
	}
}

// jwksCache fetches and caches a Supabase project's JSON Web Key Set so
// asymmetric (RS256/ES256) access tokens can be verified locally without
// a round trip to Supabase on every request.
type jwksCache struct {
	url    string
	client *http.Client

	mu        sync.Mutex
	keys      map[string]any // kid -> *rsa.PublicKey | *ecdsa.PublicKey
	algCache  map[string][]any
	fetchedAt time.Time
	ttl       time.Duration
}

type jwkSet struct {
	Keys []jwk `json:"keys"`
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

// keyFor resolves a token's signing key. The key id from the token header
// is preferred so key rotation works, but a single-key set resolves even
// when the token omits `kid`.
func (c *jwksCache) keyFor(ctx context.Context, alg, kid string) (any, error) {
	keys, err := c.get(ctx, alg)
	if err != nil {
		return nil, err
	}
	if kid != "" {
		c.mu.Lock()
		key, ok := c.keys[kid]
		c.mu.Unlock()
		if ok {
			return key, nil
		}
	}
	if len(keys) > 0 {
		return keys[0], nil
	}
	return nil, fmt.Errorf("no %s key found in JWKS at %s", alg, c.url)
}

func (c *jwksCache) get(ctx context.Context, alg string) ([]any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.keys != nil && time.Since(c.fetchedAt) < c.ttl && len(c.algCache[alg]) > 0 {
		return c.algCache[alg], nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jwks fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jwks fetch: unexpected status %d", resp.StatusCode)
	}
	var set jwkSet
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&set); err != nil {
		return nil, fmt.Errorf("jwks decode: %w", err)
	}

	parsed := map[string]any{}
	byAlg := map[string][]any{}
	for _, k := range set.Keys {
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		keyAlg := k.Alg
		if keyAlg == "" {
			keyAlg = alg
		}
		if keyAlg != alg {
			continue
		}
		key, err := k.publicKey()
		if err != nil {
			continue
		}
		kid := k.Kid
		if kid == "" {
			kid = fmt.Sprintf("idx-%d", len(parsed))
		}
		parsed[kid] = key
		byAlg[alg] = append(byAlg[alg], key)
	}
	if len(parsed) == 0 {
		return nil, errors.New("jwks contains no usable keys")
	}
	c.keys = parsed
	c.algCache = byAlg
	c.fetchedAt = time.Now()
	c.ttl = 10 * time.Minute
	return byAlg[alg], nil
}

func (k jwk) publicKey() (any, error) {
	switch k.Kty {
	case "RSA":
		nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			return nil, err
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			return nil, err
		}
		if len(nBytes) == 0 || len(eBytes) == 0 || len(eBytes) > 8 {
			return nil, errors.New("invalid RSA jwk")
		}
		e := 0
		for _, b := range eBytes {
			e = e<<8 | int(b)
		}
		return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: e}, nil
	case "EC":
		crv := k.Crv
		x, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil {
			return nil, err
		}
		y, err := base64.RawURLEncoding.DecodeString(k.Y)
		if err != nil {
			return nil, err
		}
		curve, err := ellipticCurve(crv)
		if err != nil {
			return nil, err
		}
		return &ecdsa.PublicKey{Curve: curve, X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}, nil
	default:
		return nil, fmt.Errorf("unsupported kty %q", k.Kty)
	}
}
