package main

import (
	"encoding/json"
	"errors"

	latticeplugin "github.com/LatticeNet/lattice-sdk/plugin"
)

// migrate_record builds the fleet record a legacy vpn-core or vpn-core-graph
// record becomes, under the same id, and stages it (plan sections 2.7 and 6).
// It never proposes: the UI collects share identities and proposes one plan
// over the record and its co-migrating siblings.

// migrateRecordCall serves migrate_record.
func (rt *runtime) migrateRecordCall(payload json.RawMessage) response {
	return latticeplugin.ErrorResponse(errors.New("migrate_record_unavailable: this build declares migrate_record and answers it once lane 4 lands the conversion"))
}
