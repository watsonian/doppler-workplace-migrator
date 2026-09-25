package doppler

import "strings"

// TokenKind is the documented format of a Doppler auth token.
//
// The format matters beyond identification: the API decides what a token may read
// from the actor behind it, so a personal or CLI token cannot read restricted
// secrets however its permissions are set. Naming the kind in an error is usually
// the whole diagnosis.
//
// See https://docs.doppler.com/reference/auth-token-formats
type TokenKind string

const (
	TokenKindCLI            TokenKind = "cli"
	TokenKindPersonal       TokenKind = "personal"
	TokenKindService        TokenKind = "service"
	TokenKindServiceAccount TokenKind = "service_account"
	TokenKindSCIM           TokenKind = "scim"
	TokenKindAudit          TokenKind = "audit"
	TokenKindSAIdentity     TokenKind = "service_account_identity"
	TokenKindUnknown        TokenKind = "unknown"
)

// Label renders a token kind the way it is written in the interface.
func (k TokenKind) Label() string {
	switch k {
	case TokenKindCLI:
		return "CLI token"
	case TokenKindPersonal:
		return "Personal token"
	case TokenKindService:
		return "Service token"
	case TokenKindServiceAccount:
		return "Service account token"
	case TokenKindSCIM:
		return "SCIM token"
	case TokenKindAudit:
		return "Audit token"
	case TokenKindSAIdentity:
		return "Service account identity token"
	default:
		return "Unrecognized token"
	}
}

// ReadsRestrictedSecrets reports whether a token of this kind can read secrets
// marked restricted. User-backed tokens cannot, and the API rejects the whole
// config rather than returning the readable entries.
func (k TokenKind) ReadsRestrictedSecrets() bool {
	return k == TokenKindServiceAccount || k == TokenKindSAIdentity
}

// tokenPrefixes are Doppler's documented auth token formats, longest first so that
// dp.said. is matched before the shorter dp.sa. could shadow it.
var tokenPrefixes = []struct {
	prefix string
	kind   TokenKind
}{
	{"dp.audit.", TokenKindAudit},
	{"dp.scim.", TokenKindSCIM},
	{"dp.said.", TokenKindSAIdentity},
	{"dp.ct.", TokenKindCLI},
	{"dp.pt.", TokenKindPersonal},
	{"dp.st.", TokenKindService},
	{"dp.sa.", TokenKindServiceAccount},
}

// ClassifyToken returns the documented format of a token from its prefix.
func ClassifyToken(token string) TokenKind {
	token = strings.TrimSpace(token)
	for _, p := range tokenPrefixes {
		if strings.HasPrefix(token, p.prefix) {
			return p.kind
		}
	}
	return TokenKindUnknown
}

// TokenHint renders a token as its format prefix plus its last four characters,
// e.g. "dp.ct.…a1b2". An unrecognized format yields just the last four. A token too
// short to have four characters beyond its prefix reveals none of them.
//
// This is the only form in which a token is ever reported back — to the interface,
// to an error record, or to an export.
func TokenHint(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}

	prefix := ""
	for _, p := range tokenPrefixes {
		if strings.HasPrefix(token, p.prefix) {
			prefix = p.prefix
			break
		}
	}

	rest := token[len(prefix):]
	if len(rest) < 4 {
		return prefix + "…"
	}
	return prefix + "…" + rest[len(rest)-4:]
}
