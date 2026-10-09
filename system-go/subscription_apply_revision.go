package main

import "errors"

// apply_revision is the plugin's half of an approved Sub-Store plan (design
// 28, S2): core claims the approval, then asks the plugin to apply a staged
// revision of one record. Core refuses it outside an approved plan and on
// every path but its own (lattice-server server_plugin_invoke.go), and it is
// declared now so core can name it in a plan, with a budget of zero host
// calls until S2 stages revisions and signs the count staging needs.
//
// S1 stages no revisions, so there is nothing to apply. The refusal is stated
// and costs no host call: it claims nothing, reads nothing and writes nothing.

const revisionStagingUnavailableCode = "revision_staging_unavailable"

func applyRevisionRefusal() error {
	return errors.New(revisionStagingUnavailableCode + ": this runtime stages no revisions, so there is no revision to apply; apply_revision answers from S2, when plans stage record revisions")
}
