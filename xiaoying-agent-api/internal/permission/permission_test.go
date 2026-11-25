package permission

import "testing"

func TestPermissionResponseRequiresMatchingSession(t *testing.T) {
	for _, tc := range []struct {
		name     string
		respond  func(*permissionService, PermissionRequest)
		approved bool
	}{
		{name: "grant", respond: (*permissionService).Grant, approved: true},
		{name: "deny", respond: (*permissionService).Deny, approved: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := make(chan bool, 1)
			service := &permissionService{}
			service.pendingRequests.Store("request-1", pendingPermission{
				sessionID: "owner-session",
				response:  response,
			})

			for _, sessionID := range []string{"other-session", ""} {
				tc.respond(service, PermissionRequest{ID: "request-1", SessionID: sessionID})
				select {
				case <-response:
					t.Fatalf("response from session %q was accepted", sessionID)
				default:
				}
			}

			tc.respond(service, PermissionRequest{ID: "request-1", SessionID: "owner-session"})
			select {
			case got := <-response:
				if got != tc.approved {
					t.Fatalf("approval = %v, want %v", got, tc.approved)
				}
			default:
				t.Fatal("response from the owning session was ignored")
			}
		})
	}
}
