package namespace

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/viant/mcp-protocol/authorization"
)

// Provider computes a namespace descriptor from request context/token.
type Provider interface {
	Namespace(ctx context.Context) (Descriptor, error)
}

// Claims provides access to identity claims (email/subject) when verified parsing is used.
type Claims interface {
	Email() string
	Subject() string
	Map() map[string]any
}

// ClaimsVerifier verifies the token and returns identity claims.
type ClaimsVerifier interface {
	VerifyClaims(ctx context.Context, token string) (Claims, error)
}

// ClaimsParser performs an unverified parse and returns raw claims.
type ClaimsParser interface {
	ParseUnverified(token string) (map[string]any, error)
}

// Option allows customizing the DefaultProvider.
type Option func(*DefaultProvider)

// WithClaimsVerifier supplies a verifier used when preferring identity.
func WithClaimsVerifier(v ClaimsVerifier) Option { return func(p *DefaultProvider) { p.verifier = v } }

// WithClaimsParser supplies a parser for unverified claims fallback.
func WithClaimsParser(p ClaimsParser) Option { return func(dp *DefaultProvider) { dp.parser = p } }

// WithPathConfig overrides default path configuration.
func WithPathConfig(pc PathConfig) Option { return func(dp *DefaultProvider) { dp.cfg.Path = pc } }

// DefaultProvider implements Provider using the supplied Config.
type DefaultProvider struct {
	cfg      Config
	verifier ClaimsVerifier
	parser   ClaimsParser
}

// NewProvider creates a DefaultProvider with optional customizations.
func NewProvider(cfg *Config, opts ...Option) *DefaultProvider {
	var c Config
	if cfg != nil {
		c = *cfg
	}
	applyDefaults(&c)

	dp := &DefaultProvider{cfg: c}
	for _, o := range opts {
		o(dp)
	}
	if dp.parser == nil {
		dp.parser = defaultParser{}
	}
	return dp
}

func (p *DefaultProvider) Namespace(ctx context.Context) (Descriptor, error) {
	// Default when no token context or no rule.
	token := extractTokenString(ctx)
	if strings.TrimSpace(token) == "" {
		return p.makeDefault(), nil
	}

	// Prefer identity claims when configured; otherwise hash isolation.
	if p.cfg.PreferIdentity {
		if d, ok := p.tryIdentity(ctx, token); ok {
			return p.decorate(d), nil
		}
	}

	// Fallback to token hash isolation.
	d := p.tokenHashDescriptor(token)
	return p.decorate(d), nil
}

func (p *DefaultProvider) tryIdentity(ctx context.Context, token string) (Descriptor, bool) {
	// If a verifier is provided, use it.
	if p.verifier != nil {
		if claims, err := p.verifier.VerifyClaims(ctx, token); err == nil {
			if ns := selectClaimValue(map[string]any{"email": claims.Email(), "sub": claims.Subject()}, p.cfg.ClaimKeys); ns != "" {
				return Descriptor{Name: ns, Kind: KindIdentity}, true
			}
		}
	}
	// Unverified parse fallback.
	if p.parser != nil {
		if m, err := p.parser.ParseUnverified(token); err == nil {
			if ns := selectClaimValue(m, p.cfg.ClaimKeys); ns != "" {
				return Descriptor{Name: ns, Kind: KindIdentity}, true
			}
		}
	}
	return Descriptor{}, false
}

func (p *DefaultProvider) tokenHashDescriptor(token string) Descriptor {
	h := hashHex(token, p.cfg.Hash)
	name := h
	if p.cfg.Hash.Prefix != "" {
		name = p.cfg.Hash.Prefix + h
	}
	return Descriptor{Name: name, Kind: KindTokenHash, Hash: h}
}

func (p *DefaultProvider) makeDefault() Descriptor {
	name := p.cfg.Default
	if name == "" {
		name = "default"
	}
	return Descriptor{Name: name, Kind: KindDefault, IsDefault: true}
}

func (p *DefaultProvider) decorate(d Descriptor) Descriptor {
	// Ensure a hash for path sharding; when identity, hash its Name for stability.
	if d.Hash == "" {
		d.Hash = hashHex(d.Name, p.cfg.Hash)
	}
	// Derive filesystem-friendly paths.
	pc := p.cfg.Path
	if d.Kind == KindIdentity || d.Kind == KindDefault {
		d.PathPrefix = buildPathPrefix(d.Name, pc, d.Hash)
	} else { // token-hash
		// For token-hash, PathPrefix bases on hash with optional prefix.
		base := d.Name
		if pc.Prefix != "" && !strings.HasPrefix(base, pc.Prefix) {
			base = pc.Prefix + base
		}
		d.PathPrefix = clipWithSuffix(base, pc.MaxLen, d.Hash)
	}
	if pc.ShardLevels > 0 && pc.ShardWidth > 0 {
		d.ShardedPath = buildShardedPath(d.PathPrefix, d.Hash, pc)
	} else {
		d.ShardedPath = d.PathPrefix
	}
	return d
}

// Utilities

func applyDefaults(c *Config) {
	if c.Default == "" {
		c.Default = "default"
	}
	if len(c.ClaimKeys) == 0 {
		c.ClaimKeys = []string{"email", "sub"}
	}
	if c.Hash.Algorithm == "" {
		c.Hash.Algorithm = "md5"
	}
	if c.Path.Sanitize == false {
		c.Path.Sanitize = true
	}
	if c.Path.Separator == "" {
		c.Path.Separator = "/"
	}
	if c.Path.MaxLen == 0 {
		c.Path.MaxLen = 120
	}
	if c.Path.ShardLevels < 0 {
		c.Path.ShardLevels = 0
	}
	if c.Path.ShardWidth < 0 {
		c.Path.ShardWidth = 0
	}
}

func extractTokenString(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v := ctx.Value(authorization.TokenKey)
	if v == nil {
		return ""
	}
	switch tv := v.(type) {
	case string:
		return normalizeBearer(tv)
	case *authorization.Token:
		return normalizeBearer(tv.Token)
	default:
		return ""
	}
}

func normalizeBearer(s string) string {
	v := strings.TrimSpace(s)
	ls := strings.ToLower(v)
	if strings.HasPrefix(ls, "bearer ") {
		return strings.TrimSpace(v[len("Bearer "):])
	}
	return v
}

func selectClaimValue(m map[string]any, order []string) string {
	for _, k := range order {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func hashHex(s string, cfg HashConfig) string {
	if cfg.Algorithm == "sha256" {
		h := sha256.New()
		if len(cfg.Salt) > 0 {
			h.Write(cfg.Salt)
		}
		h.Write([]byte(s))
		sum := h.Sum(nil)
		out := hex.EncodeToString(sum)
		if cfg.Truncate > 0 && cfg.Truncate < len(out) {
			return out[:cfg.Truncate]
		}
		return out
	}
	// default md5
	sum := md5.Sum(append(cfg.Salt, []byte(s)...))
	out := hex.EncodeToString(sum[:])
	if cfg.Truncate > 0 && cfg.Truncate < len(out) {
		return out[:cfg.Truncate]
	}
	return out
}

var allowed = regexp.MustCompile(`[a-z0-9_.-]`)

func sanitizeIdentity(s string) string {
	if s == "" {
		return s
	}
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "@", "_at_")
	var b strings.Builder
	b.Grow(len(s))
	lastDash := false
	for _, r := range s {
		c := string(r)
		if allowed.MatchString(c) {
			b.WriteString(c)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := b.String()
	out = strings.Trim(out, "-.")
	if out == "" {
		return "id"
	}
	return out
}

func clipWithSuffix(base string, max int, hash string) string {
	if max <= 0 || len(base) <= max {
		return base
	}
	suffix := ""
	if hash != "" {
		if len(hash) > 8 {
			hash = hash[:8]
		}
		suffix = "-" + hash
	}
	keep := max - len(suffix)
	if keep <= 0 {
		if len(suffix) > max {
			return suffix[:max]
		}
		return suffix
	}
	return base[:keep] + suffix
}

func buildPathPrefix(name string, pc PathConfig, hash string) string {
	base := name
	if pc.Sanitize {
		base = sanitizeIdentity(base)
	}
	if pc.Prefix != "" {
		base = pc.Prefix + base
	}
	return clipWithSuffix(base, pc.MaxLen, hash)
}

func buildShardedPath(prefix, hash string, pc PathConfig) string {
	if pc.ShardLevels <= 0 || pc.ShardWidth <= 0 {
		return prefix
	}
	sep := pc.Separator
	if sep == "" {
		sep = "/"
	}
	// Ensure hash is long enough; extend with itself if needed.
	needed := pc.ShardLevels * pc.ShardWidth * 2 // hex chars per shard
	h := hash
	for len(h) < needed {
		h += hash
	}
	parts := make([]string, 0, pc.ShardLevels+1)
	pos := 0
	for i := 0; i < pc.ShardLevels; i++ {
		width := pc.ShardWidth * 2
		parts = append(parts, h[pos:pos+width])
		pos += width
	}
	parts = append(parts, prefix)
	return strings.Join(parts, sep)
}

// defaultParser implements ClaimsParser using jwt v5 unverified parsing.
type defaultParser struct{}

func (defaultParser) ParseUnverified(token string) (map[string]any, error) {
	var m jwt.MapClaims
	_, _, err := new(jwt.Parser).ParseUnverified(token, &m)
	if err != nil {
		return nil, err
	}
	return m, nil
}
