package hibernation

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// Target identifies the cluster whose owning resource should be hibernated.
type Target struct {
	Key             types.NamespacedName
	UID             types.UID
	OwnerReferences []metav1.OwnerReference
}

// Hibernator applies the mutations required to hibernate a target.
type Hibernator interface {
	Hibernate(context.Context, Target) error
}
