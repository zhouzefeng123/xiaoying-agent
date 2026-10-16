package session

import "testing"

func TestShouldHideFromHistory(t *testing.T) {
	tests := []struct {
		name    string
		session Session
		want    bool
	}{
		{
			name:    "worker session hidden",
			session: Session{Title: "Worker: researcher"},
			want:    true,
		},
		{
			name:    "worker session hidden after trim",
			session: Session{Title: "  Worker: coder  "},
			want:    true,
		},
		{
			name:    "regular session visible",
			session: Session{Title: "修复侧边栏问题"},
			want:    false,
		},
		{
			name:    "empty title visible",
			session: Session{Title: ""},
			want:    false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldHideFromHistory(test.session); got != test.want {
				t.Fatalf("expected %v, got %v for title %q", test.want, got, test.session.Title)
			}
		})
	}
}
