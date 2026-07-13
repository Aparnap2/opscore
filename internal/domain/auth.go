package domain

// Role represents a user's role within a tenant.
type Role string

const (
	RoleOwner    Role = "owner"
	RoleOpsAdmin Role = "ops_admin"
	RoleReviewer Role = "reviewer"
	RoleAuditor  Role = "auditor"
	RoleViewer   Role = "viewer"
)

// Permission represents a specific action that a user may perform.
type Permission string

const (
	PermissionDocumentUpload Permission = "document:upload"
	PermissionDocumentView   Permission = "document:view"
	PermissionVendorCreate   Permission = "vendor:create"
	PermissionVendorView     Permission = "vendor:view"
	PermissionVendorApprove  Permission = "vendor:approve"
	PermissionComplianceView Permission = "compliance:view"
	PermissionMetricsView    Permission = "metrics:view"
	PermissionAuditView      Permission = "audit:view"
	PermissionReviewQueue    Permission = "review_queue:manage"
	PermissionAdmin          Permission = "admin:*"
)

// RolePermissions maps each role to its set of allowed permissions.
// The owner role gets admin:* which grants access to everything.
var RolePermissions = map[Role][]Permission{
	RoleOwner: {
		PermissionAdmin,
	},
	RoleOpsAdmin: {
		PermissionDocumentUpload,
		PermissionDocumentView,
		PermissionVendorCreate,
		PermissionVendorView,
		PermissionVendorApprove,
		PermissionComplianceView,
		PermissionMetricsView,
		PermissionAuditView,
		PermissionReviewQueue,
	},
	RoleReviewer: {
		PermissionDocumentView,
		PermissionVendorView,
		PermissionVendorApprove,
		PermissionComplianceView,
		PermissionReviewQueue,
	},
	RoleAuditor: {
		PermissionDocumentView,
		PermissionVendorView,
		PermissionComplianceView,
		PermissionAuditView,
		PermissionMetricsView,
	},
	RoleViewer: {
		PermissionDocumentView,
		PermissionVendorView,
		PermissionComplianceView,
	},
}

// User represents an authenticated user within a tenant.
type User struct {
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
	Email    string `json:"email"`
	Role     Role   `json:"role"`
	Name     string `json:"name"`
}

// HasPermission checks whether the user's role grants the given permission.
// The admin:* permission acts as a wildcard granting access to everything.
func (u *User) HasPermission(perm Permission) bool {
	perms, ok := RolePermissions[u.Role]
	if !ok {
		return false
	}
	for _, p := range perms {
		if p == PermissionAdmin || p == perm {
			return true
		}
	}
	return false
}
