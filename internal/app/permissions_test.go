package app

import (
	"testing"

	"github.com/tfrydrychewicz/ogre/internal/linkmodel"
)

func TestCanEdit(t *testing.T) {
	link := linkmodel.Link{ID: 12, Name: "roadmap", OwnerID: 1}

	tests := []struct {
		name  string
		actor Actor
		want  bool
	}{
		{"the owner", Actor{UserID: 1}, true},
		{"the owner, who is also an admin", Actor{UserID: 1, Admin: true}, true},
		{"an admin who is not the owner", Actor{UserID: 7, Admin: true}, true},
		{"someone else", Actor{UserID: 7}, false},
		{"nobody", Actor{}, false},
		{"nobody, claiming to be an admin", Actor{Admin: true}, false},
	}
	a, _ := newApp(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := a.CanEdit(tt.actor, link); got != tt.want {
				t.Errorf("CanEdit(%+v) = %v, want %v", tt.actor, got, tt.want)
			}
		})
	}

	// A link without an owner cannot exist, but if one did the zero Actor
	// must not match it by accident.
	if a.CanEdit(Actor{}, linkmodel.Link{}) {
		t.Error("the zero Actor may edit a link whose owner is 0")
	}
}

// The actor local mode acts as may edit every link it creates.
func TestLocalActorCanEditItsLinks(t *testing.T) {
	a, _ := newApp(t)
	l := mustCreate(t, a, draft("roadmap", "https://example.com"))
	if !a.CanEdit(localActor, l) {
		t.Errorf("CanEdit(%+v, %+v) = false", localActor, l)
	}
}
