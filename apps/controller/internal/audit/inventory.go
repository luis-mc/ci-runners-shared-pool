package audit

// InventoryField is one audit_log field that holds personal data, in the shape
// of a row of docs/reference/data-inventory.md (observability R6.14). That file
// is generated, so the list lives here and the generator reads it.
type InventoryField struct {
	Table string
	Field string // the AuditRow field the column is read into
	What  string
	Why   string
	Ends  string // the retention, R6.8
}

// InventoryFields are the personal fields of the audit log. Every other field
// of the row is classified in inventory_test.go as not personal.
var InventoryFields = []InventoryField{
	{Table: "audit_log", Field: "Actor", What: "GitHub login of who made the change (or system, or a machine name)", Why: "Audit: who changed what (R5.24)", Ends: "Audit retention, 400 days by default (R6.8)"},
	{Table: "audit_log", Field: "ActorID", What: "GitHub user id of who made the change", Why: "Audit: a login can be renamed, the id cannot (R5.24)", Ends: "Audit retention, 400 days by default (R6.8)"},
}
