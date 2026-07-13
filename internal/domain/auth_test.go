package domain

import (
	"testing"
)

// ---------------------------------------------------------------------------
// Role permissions table tests
// ---------------------------------------------------------------------------

func TestRolePermissions_OwnerHasAll(t *testing.T) {
	perms := RolePermissions[RoleOwner]
	hasAdmin := false
	for _, p := range perms {
		if p == PermissionAdmin {
			hasAdmin = true
			break
		}
	}
	if !hasAdmin {
		t.Error("owner role should have admin:* permission")
	}
}

func TestRolePermissions_OpsAdminHasExpected(t *testing.T) {
	expected := []Permission{
		PermissionDocumentUpload,
		PermissionDocumentView,
		PermissionVendorCreate,
		PermissionVendorView,
		PermissionVendorApprove,
		PermissionComplianceView,
		PermissionMetricsView,
		PermissionAuditView,
	}
	perms := RolePermissions[RoleOpsAdmin]
	for _, exp := range expected {
		if !containsPermission(perms, exp) {
			t.Errorf("ops_admin should have permission %s", exp)
		}
	}
	// ops_admin should NOT have admin:*
	if containsPermission(perms, PermissionAdmin) {
		t.Error("ops_admin should NOT have admin:* permission")
	}
}

func TestRolePermissions_ReviewerHasExpected(t *testing.T) {
	expected := []Permission{
		PermissionDocumentView,
		PermissionVendorView,
		PermissionVendorApprove,
		PermissionComplianceView,
	}
	perms := RolePermissions[RoleReviewer]
	for _, exp := range expected {
		if !containsPermission(perms, exp) {
			t.Errorf("reviewer should have permission %s", exp)
		}
	}
	notExpected := []Permission{
		PermissionDocumentUpload,
		PermissionVendorCreate,
		PermissionMetricsView,
		PermissionAuditView,
		PermissionAdmin,
	}
	for _, ne := range notExpected {
		if containsPermission(perms, ne) {
			t.Errorf("reviewer should NOT have permission %s", ne)
		}
	}
}

func TestRolePermissions_AuditorHasExpected(t *testing.T) {
	expected := []Permission{
		PermissionDocumentView,
		PermissionVendorView,
		PermissionComplianceView,
		PermissionAuditView,
		PermissionMetricsView,
	}
	perms := RolePermissions[RoleAuditor]
	for _, exp := range expected {
		if !containsPermission(perms, exp) {
			t.Errorf("auditor should have permission %s", exp)
		}
	}
	notExpected := []Permission{
		PermissionDocumentUpload,
		PermissionVendorCreate,
		PermissionVendorApprove,
		PermissionAdmin,
	}
	for _, ne := range notExpected {
		if containsPermission(perms, ne) {
			t.Errorf("auditor should NOT have permission %s", ne)
		}
	}
}

func TestRolePermissions_ViewerHasExpected(t *testing.T) {
	expected := []Permission{
		PermissionDocumentView,
		PermissionVendorView,
		PermissionComplianceView,
	}
	perms := RolePermissions[RoleViewer]
	for _, exp := range expected {
		if !containsPermission(perms, exp) {
			t.Errorf("viewer should have permission %s", exp)
		}
	}
	notExpected := []Permission{
		PermissionDocumentUpload,
		PermissionVendorCreate,
		PermissionVendorApprove,
		PermissionMetricsView,
		PermissionAuditView,
		PermissionAdmin,
	}
	for _, ne := range notExpected {
		if containsPermission(perms, ne) {
			t.Errorf("viewer should NOT have permission %s", ne)
		}
	}
}

// ---------------------------------------------------------------------------
// User.HasPermission tests
// ---------------------------------------------------------------------------

func TestUserHasPermission_Owner(t *testing.T) {
	user := &User{Role: RoleOwner, TenantID: "tenant-1"}
	// Owner should have all permissions via admin:*
	allPerms := []Permission{
		PermissionDocumentUpload,
		PermissionDocumentView,
		PermissionVendorCreate,
		PermissionVendorView,
		PermissionVendorApprove,
		PermissionComplianceView,
		PermissionMetricsView,
		PermissionAuditView,
		PermissionAdmin,
	}
	for _, p := range allPerms {
		if !user.HasPermission(p) {
			t.Errorf("owner should have permission %s", p)
		}
	}
}

func TestUserHasPermission_OpsAdmin(t *testing.T) {
	user := &User{Role: RoleOpsAdmin, TenantID: "tenant-1"}

	allowed := []Permission{
		PermissionDocumentUpload,
		PermissionDocumentView,
		PermissionVendorCreate,
		PermissionVendorView,
		PermissionVendorApprove,
		PermissionComplianceView,
		PermissionMetricsView,
		PermissionAuditView,
	}
	for _, p := range allowed {
		if !user.HasPermission(p) {
			t.Errorf("ops_admin should have permission %s", p)
		}
	}

	denied := []Permission{
		PermissionAdmin,
	}
	for _, p := range denied {
		if user.HasPermission(p) {
			t.Errorf("ops_admin should NOT have permission %s", p)
		}
	}
}

func TestUserHasPermission_Reviewer(t *testing.T) {
	user := &User{Role: RoleReviewer, TenantID: "tenant-1"}

	allowed := []Permission{
		PermissionDocumentView,
		PermissionVendorView,
		PermissionVendorApprove,
		PermissionComplianceView,
	}
	for _, p := range allowed {
		if !user.HasPermission(p) {
			t.Errorf("reviewer should have permission %s", p)
		}
	}

	denied := []Permission{
		PermissionDocumentUpload,
		PermissionVendorCreate,
		PermissionMetricsView,
		PermissionAuditView,
		PermissionAdmin,
	}
	for _, p := range denied {
		if user.HasPermission(p) {
			t.Errorf("reviewer should NOT have permission %s", p)
		}
	}
}

func TestUserHasPermission_Auditor(t *testing.T) {
	user := &User{Role: RoleAuditor, TenantID: "tenant-1"}

	allowed := []Permission{
		PermissionDocumentView,
		PermissionVendorView,
		PermissionComplianceView,
		PermissionAuditView,
		PermissionMetricsView,
	}
	for _, p := range allowed {
		if !user.HasPermission(p) {
			t.Errorf("auditor should have permission %s", p)
		}
	}

	denied := []Permission{
		PermissionDocumentUpload,
		PermissionVendorCreate,
		PermissionVendorApprove,
		PermissionAdmin,
	}
	for _, p := range denied {
		if user.HasPermission(p) {
			t.Errorf("auditor should NOT have permission %s", p)
		}
	}
}

func TestUserHasPermission_Viewer(t *testing.T) {
	user := &User{Role: RoleViewer, TenantID: "tenant-1"}

	allowed := []Permission{
		PermissionDocumentView,
		PermissionVendorView,
		PermissionComplianceView,
	}
	for _, p := range allowed {
		if !user.HasPermission(p) {
			t.Errorf("viewer should have permission %s", p)
		}
	}

	denied := []Permission{
		PermissionDocumentUpload,
		PermissionVendorCreate,
		PermissionVendorApprove,
		PermissionMetricsView,
		PermissionAuditView,
		PermissionAdmin,
	}
	for _, p := range denied {
		if user.HasPermission(p) {
			t.Errorf("viewer should NOT have permission %s", p)
		}
	}
}

func TestUserHasPermission_UnknownRole(t *testing.T) {
	user := &User{Role: "unknown_role", TenantID: "tenant-1"}
	if user.HasPermission(PermissionDocumentView) {
		t.Error("unknown role should not have any permissions")
	}
}

// ---------------------------------------------------------------------------
// Benchmarks
// ---------------------------------------------------------------------------

func BenchmarkHasPermission(b *testing.B) {
	user := &User{Role: RoleOpsAdmin}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		user.HasPermission(PermissionDocumentUpload)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func containsPermission(slice []Permission, item Permission) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
