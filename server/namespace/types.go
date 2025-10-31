package namespace

// Kind represents how the namespace was derived.
type Kind string

const (
	// KindDefault indicates no token or policy and the default namespace in use.
	KindDefault Kind = "default"
	// KindIdentity indicates the namespace was derived from identity claims (email/sub).
	KindIdentity Kind = "identity"
	// KindTokenHash indicates the namespace was derived from a token hash.
	KindTokenHash Kind = "token-hash"
)

// Descriptor captures computed namespace information and filesystem-friendly paths.
type Descriptor struct {
	// Name is the canonical namespace (email/sub or hash string).
	Name string
	// Kind indicates how the namespace was derived.
	Kind Kind
	// IsDefault signals that the default namespace was used.
	IsDefault bool

	// Hash is a stable hex-encoded hash of the namespace basis (token or identity).
	Hash string

	// PathPrefix is a single, filesystem-safe segment suitable for namespacing directories.
	PathPrefix string
	// ShardedPath is an optional, multi-segment path for large fan-out storage (e.g. aa/bb/prefix).
	ShardedPath string
}

// HashConfig configures fallback hashing behavior.
type HashConfig struct {
	// Algorithm selects the hash algorithm ("md5" (default) or "sha256").
	Algorithm string
	// Prefix is an optional string added before the hex hash (e.g., "tkn-").
	Prefix string
	// Salt optionally mixes extra entropy into the hash.
	Salt []byte
	// Truncate shortens the produced hex string to N characters (0 = no truncation).
	Truncate int
}

// PathConfig configures how filesystem-friendly paths are derived.
type PathConfig struct {
	// Sanitize enforces lowercasing and character filtering for identity-based names.
	Sanitize bool
	// MaxLen truncates long PathPrefix values and appends a short hash suffix (0 = unlimited).
	MaxLen int
	// ShardLevels indicates the number of shard directories to prepend (0 = none).
	ShardLevels int
	// ShardWidth indicates how many bytes per shard (converted to 2*width hex chars).
	ShardWidth int
	// Prefix optionally adds a leading label (e.g., "id-" or "tkn-") to PathPrefix when derived from claims/hash.
	Prefix string
	// Separator is used when composing ShardedPath (default "/").
	Separator string
}

// Config controls namespace derivation behavior.
type Config struct {
	// Default is the default namespace when no token or claims are available. Defaults to "default".
	Default string
	// PreferIdentity when true attempts to derive namespace from identity claims (email/sub) first.
	// When false, a token-hash is used by default for isolation.
	PreferIdentity bool
	// ClaimKeys indicates the claim lookup order for identity (e.g., ["email","sub"]).
	ClaimKeys []string
	// Hash controls token hash behavior when identity is not available or not preferred.
	Hash HashConfig
	// Path controls derivation of filesystem-friendly paths.
	Path PathConfig
}
