package doppler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// ListSecrets returns all secrets for a config using raw values.
func (c *Client) ListSecrets(ctx context.Context, project, config string) (SecretsMap, error) {
	params := url.Values{
		"project": {project},
		"config":  {config},
		// The server defaults this to true and merges inherited secrets into the
		// same map with nothing marking them as inherited. Migrating that map would
		// write them to the target as local values, which shadow inherited ones —
		// quietly severing the inheritance the migration sets up a phase later.
		"include_inherited_secrets": {"false"},
		// Dynamic secrets are runtime leases, not config. The server already
		// defaults this to false; it is sent explicitly so the intent is visible.
		"include_dynamic_secrets": {"false"},
	}

	result, _, err := c.GetSecrets(ctx, "/configs/config/secrets", params)
	if err != nil {
		return nil, annotateRestrictedReadError(err)
	}

	var resp struct {
		Secrets SecretsMap `json:"secrets"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, fmt.Errorf("parsing secrets: %w", err)
	}

	return resp.Secrets, nil
}

// UpdateSecretsWithMetadata updates secrets using the change_requests format,
// which is the only write path that carries each secret's visibility.
//
// Notes are not settable here — the endpoint strips them — so they still go
// through UpdateSecretNote separately.
func (c *Client) UpdateSecretsWithMetadata(ctx context.Context, project, config string, changes []SecretChangeRequest) error {
	body := map[string]interface{}{
		"project":         project,
		"config":          config,
		"change_requests": changes,
	}

	_, _, err := c.Post(ctx, "/configs/config/secrets", body)
	return redactSubmittedValues(err, changes)
}

// redactSubmittedValues strips any submitted secret value out of an error before
// it can travel any further.
//
// This is the one request in the migration whose body is secret material, and an
// error from it is the one place a target could hand a value back. It does:
// `Invalid reference format: <the whole raw value>` is a real response from the
// secrets endpoint when a value contains a nested reference, and from there the
// error text reaches the checkpoint file on disk, the logs, the event stream and
// the CSV export.
//
// Rather than recognize that one message, this removes every value that was
// actually sent. Anything the target echoes back — that case, a future one, or a
// proxy replaying the request — is covered, because the redaction is driven by
// what we know we submitted rather than by what we expect to receive.
func redactSubmittedValues(err error, changes []SecretChangeRequest) error {
	if err == nil {
		return nil
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return err
	}

	for i := range changes {
		value := changes[i].Value
		// A short value would match too much of the surrounding text to replace
		// safely, and is not worth protecting at the cost of mangling the message.
		if value == nil || len(*value) < minRedactableValue {
			continue
		}
		apiErr.Body = strings.ReplaceAll(apiErr.Body, *value, redactedMarker)
		apiErr.Message = strings.ReplaceAll(apiErr.Message, *value, redactedMarker)
	}

	return err
}

const (
	// minRedactableValue is the shortest value worth removing from an error. A
	// two-character secret appears by chance in ordinary prose, and replacing it
	// everywhere would corrupt the message while protecting almost nothing.
	minRedactableValue = 6
	redactedMarker     = "[value redacted]"
)

// UpdateSecretNote sets a secret's note.
//
// Notes are project-scoped, not config-scoped: the same secret name carries one
// note across every config in the project. The config-scoped endpoint is marked
// deprecated in the API ("use the projects/project/note endpoint instead"), so this
// uses the project-level one and the caller writes each note once per project
// rather than once per config.
//
// Notes cannot ride along with the secret write: the secrets endpoint accepts a
// note field in change_requests and then explicitly discards it.
func (c *Client) UpdateSecretNote(ctx context.Context, project, secretName, note string) error {
	body := map[string]string{
		"project": project,
		"secret":  secretName,
		"note":    note,
	}

	params := url.Values{"project": {project}}

	_, _, err := c.Post(ctx, "/projects/project/note?"+params.Encode(), body)
	return err
}

// annotateRestrictedReadError explains a 403 caused by restricted secrets.
//
// The API decides what a token may read from the actor behind it, not from its
// permissions: a user-backed token (dp.pt., or a dp.ct. CLI token) is limited to
// unmasked and masked secrets, while a service account token may also read
// restricted ones. Reading a config that holds a restricted secret with a
// user-backed token fails for the whole config, not just that secret — so a single
// restricted secret anywhere aborts the migration with an opaque 403.
func annotateRestrictedReadError(err error) error {
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusForbidden {
		return err
	}
	if !strings.Contains(strings.ToLower(apiErr.Error()), "restricted") {
		return err
	}

	return fmt.Errorf("%w — this config contains restricted secrets, which a user-backed "+
		"token (dp.pt. or dp.ct.) cannot read. Re-run with a service account token (dp.sa.) "+
		"for the source instance", err)
}
