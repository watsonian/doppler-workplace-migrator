package migration

import (
	"errors"
	"net/http"
	"strings"

	"github.com/doppler/workplace-migrator/internal/doppler"
)

// ErrorClassKey identifies a kind of failure.
//
// Errors are grouped by class rather than listed individually because a migration
// of any size produces hundreds of failures with a handful of causes. Two hundred
// configs rejected for the same reason are one problem with one fix, and listing
// them flat hides that.
type ErrorClassKey string

const (
	// ClassRestrictedRead is the 403 a user-backed token gets when a config holds
	// a restricted secret. The API rejects the whole config, not the one value.
	ClassRestrictedRead ErrorClassKey = "restricted_read_denied"
	// ClassRateLimited is a 429 that survived every retry.
	ClassRateLimited ErrorClassKey = "rate_limit_exhausted"
	// ClassAlreadyExists is a 409 from writing something the target already has.
	ClassAlreadyExists ErrorClassKey = "already_exists"
	// ClassSubjectNotFound is a reference to a user, group or service account that
	// does not exist on the target — usually because SCIM has not run yet.
	ClassSubjectNotFound ErrorClassKey = "subject_not_found"
	// ClassPermissionDenied is a 403 that is not about restricted secrets.
	ClassPermissionDenied ErrorClassKey = "permission_denied"
	// ClassEntitlementMissing is the target's plan not covering a source feature.
	ClassEntitlementMissing ErrorClassKey = "entitlement_missing"
	// ClassValidationRejected is a 4xx the target raised against the payload.
	ClassValidationRejected ErrorClassKey = "validation_rejected"
	// ClassServerError is a 5xx. On this API an unresolvable policy subject shows
	// up here rather than as a 4xx, so the cause text says so.
	ClassServerError ErrorClassKey = "server_error"
	// ClassNetwork is a transport failure — no HTTP response at all.
	ClassNetwork ErrorClassKey = "network"
	// ClassUnresolvedReference is a warning, not a failure: a secret referencing a
	// config that has not been migrated yet. It is written and resolves later.
	ClassUnresolvedReference ErrorClassKey = "unresolved_reference"
	// ClassStaleGather is a retry with nothing left to send: secrets are wiped
	// from memory after every attempt, so a failed config comes back empty.
	ClassStaleGather ErrorClassKey = "stale_gather"
	// ClassUnknown is anything not recognized. It exists so nothing is dropped.
	ClassUnknown ErrorClassKey = "unknown"
)

// RemedyKind is the shape of the fix a class needs, which is what the interface
// renders. They are genuinely different: a credential, a setting, a choice, or
// something done outside the tool.
type RemedyKind string

const (
	// RemedyToken takes a replacement token and re-runs the affected items.
	RemedyToken RemedyKind = "token"
	// RemedyPace re-runs the affected items with a deliberate gap between
	// requests. The migration writes sequentially, so there is no concurrency to
	// reduce — what slows it down is pacing.
	RemedyPace RemedyKind = "pace"
	// RemedyDecision needs a choice per item before anything can be re-run.
	RemedyDecision RemedyKind = "decision"
	// RemedyRecheck re-reads the target and re-runs, for things fixed elsewhere.
	RemedyRecheck RemedyKind = "recheck"
	// RemedyNone cannot be fixed from here. It is reported and carried forward.
	RemedyNone RemedyKind = "none"
)

// Severity separates failures from things merely worth knowing. A warning class
// did not stop anything from being written.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Remedy describes how a whole class is cleared.
//
// Label carries no counts. The count belongs in the sentence above the button,
// because a label baked with a number goes stale while the run continues.
type Remedy struct {
	Kind   RemedyKind `json:"kind"`
	Label  string     `json:"label"`
	Detail string     `json:"detail"`
	// Choices are the options for a RemedyDecision, in the order they should read.
	Choices []RemedyChoice `json:"choices,omitempty"`
}

// RemedyChoice is one option for a decision-shaped remedy.
type RemedyChoice struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// ErrorClass is the static description of a failure kind: what it is, why it
// happens, and how it is cleared.
type ErrorClass struct {
	Key      ErrorClassKey `json:"key"`
	Title    string        `json:"title"`
	Cause    string        `json:"cause"`
	Severity Severity      `json:"severity"`
	Remedy   Remedy        `json:"remedy"`
}

// errorClasses is the full taxonomy. Cause text is written to be read by whoever
// is running the migration, so it says what went wrong and what to do rather than
// restating the status code.
var errorClasses = map[ErrorClassKey]ErrorClass{
	ClassRestrictedRead: {
		Key:      ClassRestrictedRead,
		Title:    "Restricted read denied",
		Severity: SeverityError,
		Cause: "The source token is user-backed. Personal and CLI tokens cannot read secrets marked " +
			"restricted, and Doppler rejects the whole config rather than returning the readable " +
			"entries — so a config with one restricted value fails entirely. A service account token " +
			"with access to these projects reads them without trouble.",
		Remedy: Remedy{
			Kind:   RemedyToken,
			Label:  "Supply Token And Re-run",
			Detail: "Supply a source service account token with access to these projects. The affected configs are re-read and written; the run continues meanwhile.",
		},
	},
	ClassRateLimited: {
		Key:      ClassRateLimited,
		Title:    "Rate limit exhausted",
		Severity: SeverityError,
		Cause: "Every retry was used before the rate limit window reset. This is load, not " +
			"configuration — re-running the affected items at a slower pace will land them.",
		Remedy: Remedy{
			Kind:   RemedyPace,
			Label:  "Re-run Slower",
			Detail: "Re-run the affected items with a fixed gap between requests. The run takes longer and nothing already written is touched.",
		},
	},
	ClassAlreadyExists: {
		Key:      ClassAlreadyExists,
		Title:    "Already exists on target",
		Severity: SeverityError,
		Cause: "Something of this name was already on the target. The migrator will not overwrite " +
			"it without being told to, because the existing contents may be in use.",
		Remedy: Remedy{
			Kind:   RemedyDecision,
			Label:  "Choose Per Item",
			Detail: "Decide what happens to each item. Keeping the target's version leaves it untouched and records the divergence.",
			Choices: []RemedyChoice{
				{Value: "keep_target", Label: "Keep The Target's Version", Description: "Leave the existing item alone and note it in the summary."},
				{Value: "overwrite", Label: "Overwrite With The Source", Description: "Write the source contents over what is there now."},
			},
		},
	},
	ClassSubjectNotFound: {
		Key:      ClassSubjectNotFound,
		Title:    "Subject not found on target",
		Severity: SeverityError,
		Cause: "This references a user, group or service account that does not exist on the target. " +
			"Users are normally created by SCIM rather than by the migrator, so this usually means " +
			"provisioning has not caught up yet.",
		Remedy: Remedy{
			Kind:   RemedyRecheck,
			Label:  "Re-check And Re-run",
			Detail: "Once the subjects exist on the target, re-check and the affected items are re-run.",
		},
	},
	ClassPermissionDenied: {
		Key:      ClassPermissionDenied,
		Title:    "Permission denied",
		Severity: SeverityError,
		Cause: "The token was rejected for this operation. Check that it has the access the " +
			"operation needs on the instance it was used against.",
		Remedy: Remedy{
			Kind:   RemedyToken,
			Label:  "Supply Token And Re-run",
			Detail: "Supply a token with the access this operation needs. The affected items are re-run.",
		},
	},
	ClassEntitlementMissing: {
		Key:      ClassEntitlementMissing,
		Title:    "Not enabled on the target",
		Severity: SeverityError,
		Cause: "The target instance does not have this feature enabled, so it cannot accept what " +
			"the source has. Groups need the user_groups and team_acls entitlements; change request " +
			"policies also have a count limit.",
		Remedy: Remedy{
			Kind:   RemedyRecheck,
			Label:  "Re-check And Re-run",
			Detail: "Once the entitlement is enabled on the target, re-check and the affected items are re-run.",
		},
	},
	ClassValidationRejected: {
		Key:      ClassValidationRejected,
		Title:    "Rejected by the target",
		Severity: SeverityError,
		Cause: "The target refused the request as invalid. The response body below is the target's " +
			"own explanation and is usually specific about which field it objected to.",
		Remedy: Remedy{
			Kind:   RemedyRecheck,
			Label:  "Re-check And Re-run",
			Detail: "Re-run the affected items once the underlying objection is addressed.",
		},
	},
	ClassServerError: {
		Key:      ClassServerError,
		Title:    "Target returned a server error",
		Severity: SeverityError,
		Cause: "The target failed to handle the request. One known case looks like this rather " +
			"than like a 4xx: a change request policy naming a reviewer the target cannot resolve " +
			"returns a 500, so check that the policy's subjects exist before reading this as a bug.",
		Remedy: Remedy{
			Kind:   RemedyRecheck,
			Label:  "Re-check And Re-run",
			Detail: "Re-run the affected items. If they fail the same way again, the response body is what to report.",
		},
	},
	ClassNetwork: {
		Key:      ClassNetwork,
		Title:    "Could not reach the instance",
		Severity: SeverityError,
		Cause: "The request never got a response. This is connectivity between the migrator and " +
			"the instance rather than anything about the data.",
		Remedy: Remedy{
			Kind:   RemedyRecheck,
			Label:  "Re-check And Re-run",
			Detail: "Re-run the affected items once the instance is reachable again.",
		},
	},
	ClassUnresolvedReference: {
		Key:      ClassUnresolvedReference,
		Title:    "Reference not yet migrated",
		Severity: SeverityWarning,
		Cause: "This secret references a config that has not been migrated yet. It is written as-is " +
			"and resolves once the referenced config lands. Nothing was lost — this is listed so the " +
			"order is visible, not because anything failed.",
		Remedy: Remedy{
			Kind:   RemedyNone,
			Label:  "",
			Detail: "No action needed. These resolve on their own once every config is written.",
		},
	},
	ClassStaleGather: {
		Key:      ClassStaleGather,
		Title:    "Nothing left to write",
		Severity: SeverityError,
		Cause: "Secret values are wiped from memory after every write attempt, so a config that " +
			"failed once comes back with nothing left to send. Writing nothing and calling it a " +
			"success would be silent data loss, so it is reported instead. Re-run Gather to read " +
			"the values again before retrying.",
		Remedy: Remedy{
			Kind:   RemedyRecheck,
			Label:  "Re-run Gather",
			Detail: "Read the source again, then retry the affected configs. Nothing already written is touched.",
		},
	},
	ClassUnknown: {
		Key:      ClassUnknown,
		Title:    "Unrecognized failure",
		Severity: SeverityError,
		Cause: "The migrator does not have a specific explanation for this one. The evidence below " +
			"is everything it knows: the request made, the token used, and what came back.",
		Remedy: Remedy{
			Kind:   RemedyRecheck,
			Label:  "Re-check And Re-run",
			Detail: "Re-run the affected items. The response body is what to report if it repeats.",
		},
	},
}

// LookupErrorClass returns the description of a class, falling back to the unknown
// class so a caller never has to handle a missing entry.
func LookupErrorClass(key ErrorClassKey) ErrorClass {
	if c, ok := errorClasses[key]; ok {
		return c
	}
	return errorClasses[ClassUnknown]
}

// ClassifyError works out which class a failure belongs to.
//
// Side says which instance the request went to, because the same status means
// different things in each direction: a 403 reading the source is usually about
// restricted secrets, while a 403 writing the target is usually about access.
func ClassifyError(err error, side string) ErrorClassKey {
	if err == nil {
		return ""
	}

	var apiErr *doppler.APIError
	if !errors.As(err, &apiErr) {
		return ClassNetwork
	}

	text := strings.ToLower(apiErr.Message + " " + apiErr.Body)

	switch {
	case apiErr.StatusCode == http.StatusTooManyRequests:
		return ClassRateLimited

	case apiErr.StatusCode == http.StatusForbidden:
		// A restricted-secret 403 is recognizable two ways: the API says so, or the
		// token simply cannot read restricted values and we were reading secrets.
		if strings.Contains(text, "restricted") {
			return ClassRestrictedRead
		}
		if side == SideSource && !apiErr.TokenKind.ReadsRestrictedSecrets() &&
			strings.Contains(strings.ToLower(apiErr.URL), "/secrets") {
			return ClassRestrictedRead
		}
		return ClassPermissionDenied

	case apiErr.StatusCode == http.StatusConflict:
		return ClassAlreadyExists

	case apiErr.StatusCode == http.StatusNotFound:
		return ClassSubjectNotFound

	case apiErr.StatusCode == http.StatusPaymentRequired,
		strings.Contains(text, "entitlement"),
		strings.Contains(text, "not enabled"),
		strings.Contains(text, "upgrade"):
		return ClassEntitlementMissing

	case apiErr.StatusCode >= 500:
		return ClassServerError

	case apiErr.StatusCode >= 400:
		return ClassValidationRejected
	}

	return ClassUnknown
}

// Side names which instance a request went to.
const (
	SideSource = "source"
	SideTarget = "target"
)
