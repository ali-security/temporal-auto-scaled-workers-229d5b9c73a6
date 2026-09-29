package computeprovider

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// classifyGCPFailure maps a Cloud Run (gRPC) error onto a FailureClass along two
// axes: what kind of failure it was, read from the gRPC status code, and whose
// config caused it, read from errWCIOwned. Errors carrying no gRPC status (a
// transport failure, or a local error before the call) fall back to the ownership
// axis.
func classifyGCPFailure(err error) FailureClass {
	if err == nil {
		return FailureUnclassified
	}

	// A cancelled request tells us nothing about either axis; don't blame it on
	// whoever's config happened to be in play.
	if errors.Is(err, context.Canceled) {
		return FailureUnclassified
	}

	// Ownership separates client-fault from WCI-fault errors. A throttled or
	// server-side failure is the provider's regardless of whose config we used.
	wciOwned := errors.Is(err, errWCIOwned)
	ownerFault := FailureRejected
	if wciOwned {
		ownerFault = FailureInternal
	}

	if st, ok := status.FromError(err); ok {
		// Server-side and transport faults are the provider's regardless of
		// ownership, so they are checked before the fault split.
		switch st.Code() {
		case codes.Unavailable, codes.DeadlineExceeded, codes.Internal, codes.Unknown:
			return FailureUnavailable
		case codes.ResourceExhausted:
			return FailureThrottled
		case codes.Unauthenticated:
			// A rejected token is worker-controller's own credential problem. A token
			// we never managed to mint reaches us as a recorded cause instead; see
			// classifyTokenFetchFailure.
			return FailureInternal
		case codes.Canceled, codes.OK:
			return FailureUnclassified
		default:
			// Client faults: narrowed by ownership below.
		}
		// Remaining codes are client faults. Our own missing resources and denied
		// permissions page the same on-call the same way, so there is nothing to
		// gain from narrowing them.
		if wciOwned {
			return FailureInternal
		}
		switch st.Code() {
		case codes.NotFound:
			return FailureNotFound
		case codes.PermissionDenied:
			return FailureAccessDenied
		default:
			return FailureRejected
		}
	}

	// No modelled gRPC status: the request either failed in transport or never
	// left the process. A transport deadline is an availability problem; local
	// validation failures fall through to ownerFault.
	if errors.Is(err, context.DeadlineExceeded) {
		return FailureUnavailable
	}
	return ownerFault
}

// impersonateStatusRE reads the status the IAM Credentials endpoint returned out
// of an impersonation error. The library builds these with fmt.Errorf and exports
// no error type, so the code survives only in the message.
var impersonateStatusRE = regexp.MustCompile(`status code (\d{3})`)

// classifyTokenFetchFailure classifies a failure to mint credentials. Unlike a
// rejected token, an unreachable or throttled token endpoint is the provider's
// fault, not ours.
func classifyTokenFetchFailure(err error) FailureClass {
	m := impersonateStatusRE.FindStringSubmatch(err.Error())
	if m == nil {
		return FailureInternal
	}
	code, convErr := strconv.Atoi(m[1])
	if convErr != nil {
		return FailureInternal
	}
	switch {
	case code >= http.StatusInternalServerError:
		return FailureUnavailable
	case code == http.StatusTooManyRequests:
		return FailureThrottled
	default:
		return FailureInternal
	}
}
