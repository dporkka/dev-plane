package artifacts

import "strings"

type MergeStrategy string

const (
	MergeTextThreeWay  MergeStrategy = "text_3way"
	MergeStructured    MergeStrategy = "structured"
	MergeExclusiveLease MergeStrategy = "exclusive_lease"
	MergeForkVariants  MergeStrategy = "fork_variants"
	MergeCustom        MergeStrategy = "custom"
)

type CollaborationPolicy struct {
	MergeStrategy MergeStrategy `json:"merge_strategy"`
	LeaseRequired bool          `json:"lease_required"`
	ForkAllowed   bool          `json:"fork_allowed"`
}

// DefaultCollaborationPolicy reflects what Dev Plane can safely do today,
// rather than promising merges that are not implemented yet. Text is directly
// mergeable. Binary/structured formats use short-lived write leases while still
// permitting speculative variants for agent swarms.
func DefaultCollaborationPolicy(artifact Artifact) CollaborationPolicy {
	mediaType := strings.ToLower(artifact.Descriptor.MediaType)
	if artifact.Kind == KindText || strings.HasPrefix(mediaType, "text/") {
		return CollaborationPolicy{
			MergeStrategy: MergeTextThreeWay,
			LeaseRequired: false,
			ForkAllowed:   true,
		}
	}
	switch artifact.Kind {
	case KindDocument, KindSpreadsheet, KindPresentation:
		return CollaborationPolicy{
			MergeStrategy: MergeExclusiveLease,
			LeaseRequired: true,
			ForkAllowed:   true,
		}
	case KindPDF, KindImage, KindAudio, KindVideo, KindThreeD, KindDataset, KindBinary:
		return CollaborationPolicy{
			MergeStrategy: MergeExclusiveLease,
			LeaseRequired: true,
			ForkAllowed:   true,
		}
	default:
		return CollaborationPolicy{
			MergeStrategy: MergeCustom,
			LeaseRequired: true,
			ForkAllowed:   true,
		}
	}
}
