package audit

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// notPersonal are the fields of an audit row that hold no personal data: a
// time, the kind of change, what it touched and its result. Detail is built
// from named fields and redacted; it never carries a login (see Redact).
var notPersonal = map[string]bool{
	"ID": true, "At": true, "Source": true, "Action": true, "TargetType": true, "TargetID": true, "Detail": true, "Result": true,
}

// TestAuditFieldsListedInDataInventory (R6.14) fails when the audit row gains a
// field that is neither listed as personal in InventoryFields nor named as not
// personal here, so a new personal field cannot skip the data inventory.
func TestAuditFieldsListedInDataInventory(t *testing.T) {
	personal := map[string]bool{}
	for _, f := range InventoryFields {
		if f.Table != "audit_log" || f.What == "" || f.Why == "" || f.Ends == "" {
			t.Errorf("inventory row incomplete: %+v", f)
		}
		personal[f.Field] = true
	}
	for _, typ := range []reflect.Type{reflect.TypeOf(store.AuditRow{}), reflect.TypeOf(store.AuditEntry{})} {
		for i := 0; i < typ.NumField(); i++ {
			n := typ.Field(i).Name
			if !personal[n] && !notPersonal[n] {
				t.Errorf("%s.%s is not classified: add it to InventoryFields (personal) or notPersonal (internal/audit/inventory_test.go)", typ.Name(), n)
			}
		}
	}
	for n := range personal {
		if _, ok := reflect.TypeOf(store.AuditRow{}).FieldByName(n); !ok {
			t.Errorf("InventoryFields names %s, which AuditRow does not have", n)
		}
	}
	// The inventory file is generated (docs/reference). When it is there, it
	// must name the audit table and each personal field.
	path := filepath.Join("..", "..", "..", "..", "docs", "reference", "data-inventory.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Logf("%s is not generated yet; the generator must read audit.InventoryFields", path)
		return
	}
	for _, f := range InventoryFields {
		if !strings.Contains(string(data), "audit_log") || !strings.Contains(string(data), f.What) {
			t.Errorf("data-inventory.md lacks the audit_log row %q", f.What)
		}
	}
}
