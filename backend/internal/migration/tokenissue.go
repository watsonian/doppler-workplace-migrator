package migration

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/doppler/workplace-migrator/internal/doppler"
)

// TokenIssuer creates tokens on the target and records the fact in the ledger.
//
// The value it returns goes to the caller and nowhere else. It is not written to
// the ledger, the checkpoint, the error store or the log — there is deliberately
// no field on any persisted type that could hold one.

// IssuedToken is a token that has just been created, on its way to being shown
// once. It exists for the length of one HTTP response.
type IssuedToken struct {
	Key       string     `json:"key"`
	Kind      TokenKind  `json:"kind"`
	Name      string     `json:"name"`
	Scope     string     `json:"scope"`
	Access    string     `json:"access,omitempty"`
	Value     string     `json:"value"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Shortened bool       `json:"expiry_shortened"`
	IssuedAt  time.Time  `json:"issued_at"`

	// ReplacedAt is set by a reroll, naming when the token it replaced was made.
	ReplacedAt *time.Time `json:"replaced_at,omitempty"`
}

// TokenIssuer issues and rerolls against one target.
type TokenIssuer struct {
	target *doppler.Client
	ledger *TokenLedger
	now    func() time.Time
}

// NewTokenIssuer builds an issuer.
func NewTokenIssuer(target *doppler.Client, ledger *TokenLedger) *TokenIssuer {
	return &TokenIssuer{target: target, ledger: ledger, now: func() time.Time { return time.Now().UTC() }}
}

// Issue creates the token on the target and returns its value once.
func (i *TokenIssuer) Issue(ctx context.Context, key string) (*IssuedToken, error) {
	rec, ok := i.ledger.Get(key)
	if !ok {
		return nil, fmt.Errorf("no such token: %s", key)
	}
	if rec.Disposition == DispIssued {
		return nil, fmt.Errorf("this token has already been issued — reroll it to replace it")
	}
	if rec.PreexistingOnTarget {
		return nil, fmt.Errorf("a token called %q already exists on the target — reroll it rather than adding a second", rec.Name)
	}

	return i.create(ctx, rec, nil)
}

// Reroll deletes what the migrator created on the target and issues a
// replacement. The source token is never touched.
func (i *TokenIssuer) Reroll(ctx context.Context, key string) (*IssuedToken, error) {
	rec, ok := i.ledger.Get(key)
	if !ok {
		return nil, fmt.Errorf("no such token: %s", key)
	}
	if rec.Disposition != DispIssued && !rec.PreexistingOnTarget {
		return nil, fmt.Errorf("this token has not been issued on the target, so there is nothing to replace")
	}

	slug := rec.TargetSlug
	if slug == "" {
		// A token found on the target rather than issued here has no slug on
		// record. It has to be looked up, and an ambiguous answer is refused
		// rather than guessed — deleting the wrong token is not recoverable.
		found, err := i.findTargetSlug(ctx, &rec)
		if err != nil {
			return nil, err
		}
		slug = found
	}

	if err := i.deleteFromTarget(ctx, &rec, slug); err != nil {
		return nil, fmt.Errorf("could not delete the existing token, so no replacement was created: %w", err)
	}

	return i.create(ctx, rec, rec.IssuedAt)
}

// create does the work both paths share.
func (i *TokenIssuer) create(ctx context.Context, rec TokenRecord, replaced *time.Time) (*IssuedToken, error) {
	now := i.now()

	expiry, shortened, expired := ClampExpiry(rec.SourceExpiresAt, now)
	if expired {
		return nil, fmt.Errorf("this token expired on the source on %s — reissuing it would grant access the source no longer has",
			rec.SourceExpiresAt.Format("2006-01-02"))
	}

	out := &IssuedToken{
		Key: rec.Key, Kind: rec.Kind, Name: rec.Name, Scope: rec.Scope(),
		Access: rec.Access, ExpiresAt: expiry, Shortened: shortened,
		IssuedAt: now, ReplacedAt: replaced,
	}

	switch rec.Kind {
	case KindServiceAccountToken:
		account, err := i.targetAccount(ctx, &rec)
		if err != nil {
			i.recordFailure(rec.Key, err)
			return nil, err
		}
		created, err := i.target.CreateServiceAccountToken(ctx, account, rec.Name, expiry)
		if err != nil {
			i.recordFailure(rec.Key, err)
			return nil, err
		}
		out.Value = created.APIKey
		if err := i.ledger.MarkIssued(rec.Key, created.Slug, expiry, shortened, now); err != nil {
			return nil, err
		}

	default:
		created, err := i.target.CreateServiceToken(ctx, rec.Project, rec.Config, rec.Name, tokenAccessForCreate(rec.Access), expiry)
		if err != nil {
			i.recordFailure(rec.Key, err)
			return nil, err
		}
		out.Value = created.Key
		if err := i.ledger.MarkIssued(rec.Key, created.Slug, expiry, shortened, now); err != nil {
			return nil, err
		}
	}

	return out, nil
}

func (i *TokenIssuer) deleteFromTarget(ctx context.Context, rec *TokenRecord, slug string) error {
	if rec.Kind == KindServiceAccountToken {
		account, err := i.targetAccount(ctx, rec)
		if err != nil {
			return err
		}
		return i.target.DeleteServiceAccountToken(ctx, account, slug)
	}
	return i.target.DeleteServiceToken(ctx, rec.Project, rec.Config, slug)
}

// targetAccount finds the target's copy of the token's service account.
//
// The slug on the record is the source's: a service account's slug is a UUID
// assigned when it is created, so the migrated account has a different one.
// Name is what carries across — the executor matches accounts the same way.
func (i *TokenIssuer) targetAccount(ctx context.Context, rec *TokenRecord) (string, error) {
	return ResolveTargetServiceAccount(ctx, i.target, rec.ServiceAccountName)
}

// ServiceAccountLister is the one call ResolveTargetServiceAccount needs.
type ServiceAccountLister interface {
	ListServiceAccounts(ctx context.Context) ([]doppler.ServiceAccount, error)
}

// ResolveTargetServiceAccount returns the slug of the target's service account
// with this name. Two with the same name is refused rather than guessed at: a
// token issued to the wrong account carries the wrong account's role.
func ResolveTargetServiceAccount(ctx context.Context, target ServiceAccountLister, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("the service account's name was not recorded — gather again so it can be found on the target")
	}
	accounts, err := target.ListServiceAccounts(ctx)
	if err != nil {
		return "", fmt.Errorf("could not list the target's service accounts: %w", err)
	}
	var found []string
	for _, sa := range accounts {
		if sa.Name == name {
			found = append(found, sa.Slug)
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("service account %q is not on the target yet — migrate it first", name)
	case 1:
		return found[0], nil
	default:
		return "", fmt.Errorf("the target has %d service accounts named %q, so which one this token belongs to is ambiguous", len(found), name)
	}
}

// findTargetSlug locates a token on the target by name. Name is all there is to
// match on, so two tokens sharing one is refused rather than resolved.
func (i *TokenIssuer) findTargetSlug(ctx context.Context, rec *TokenRecord) (string, error) {
	var names []string
	var slugs []string

	if rec.Kind == KindServiceAccountToken {
		account, err := i.targetAccount(ctx, rec)
		if err != nil {
			return "", err
		}
		tokens, err := i.target.ListServiceAccountTokens(ctx, account)
		if err != nil {
			return "", fmt.Errorf("could not list the target's tokens for %s: %w", rec.Scope(), err)
		}
		for _, t := range tokens {
			name := t.Name
			if name == "" {
				name = t.Slug
			}
			names = append(names, name)
			slugs = append(slugs, t.Slug)
		}
	} else {
		tokens, err := i.target.ListServiceTokens(ctx, rec.Project, rec.Config)
		if err != nil {
			return "", fmt.Errorf("could not list the target's tokens for %s: %w", rec.Scope(), err)
		}
		for _, t := range tokens {
			names = append(names, t.Name)
			slugs = append(slugs, t.Slug)
		}
	}

	var found []string
	for idx, name := range names {
		if name == rec.Name {
			found = append(found, slugs[idx])
		}
	}

	switch len(found) {
	case 0:
		return "", fmt.Errorf("no token called %q exists on the target in %s any more", rec.Name, rec.Scope())
	case 1:
		return found[0], nil
	default:
		return "", fmt.Errorf("%d tokens in %s are called %q, and the API gives nothing else to tell them apart — delete the one you do not want in the target dashboard first",
			len(found), rec.Scope(), rec.Name)
	}
}

// recordFailure keeps the row undecided — a failure is not a decision — while
// remembering why, so the reason survives a reload.
func (i *TokenIssuer) recordFailure(key string, err error) {
	_ = i.ledger.MarkFailed(key, boundReason(explainTokenFailure(err)))
}

// explainTokenFailure names the one failure whose cause is least guessable from
// its status code.
func explainTokenFailure(err error) string {
	var apiErr *doppler.APIError
	if !errors.As(err, &apiErr) {
		return err.Error()
	}

	switch apiErr.StatusCode {
	case 403:
		return "the target refused the write: this config's trusted IPs no longer include the migrator, and issuing a token goes through the same gate as any other config write"
	case 429:
		return "the target rate limited the request — nothing was created"
	default:
		return apiErr.Error()
	}
}
