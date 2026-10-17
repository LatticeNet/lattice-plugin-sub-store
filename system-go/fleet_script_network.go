package main

import "fmt"

// Scripts on fleet-bound records run without a network (S2-SR-2).
//
// The script network is granted per method (script_network_policy.go): render
// and publish get it, because serving a provider record's chain for real is
// their job. A fleet-bound record's chain sees the catalogue's endpoints,
// labels and Reality material, which only a vpncore:read holder may read,
// while its author needs only substore:admin; and it runs when a share is
// served and when core previews a staged revision for a plan, before anyone
// approved it. With a network, one authored step could send that data
// anywhere. Until S3's isolate gives scripts a network-free shape for fleet
// nodes, every render or preview that reaches fleet nodes runs with the
// network withheld, and a step that asks for it fails the call with
// fleet_script_network_unavailable, leading the message the editor and core
// read, instead of being skipped. processNodes withholds it as well, so no
// path that runs a script over fleet nodes can reach the network even if it
// is not wrapped here.

// withFleetScriptNetworkWithheld runs fn with the invocation's script network
// withheld and answers fleet_script_network_unavailable when any script asked
// for the network meanwhile, whatever fn answered: a script that caught the
// refusal and went on still fails the call. label names the record ("collection
// \"id\"").
func withFleetScriptNetworkWithheld[T any](rt *runtime, label string, fn func() (T, error)) (T, error) {
	engine := rt.subStoreEngine()
	if rt.engine == nil {
		// Every engine call fn makes has to find the refusal installed here.
		rt.engine = engine
	}
	refusal, restore := engine.withholdScriptHTTP()
	defer restore()
	before := refusal.refusedCalls()
	out, err := fn()
	if asked := refusal.refusedCalls() - before; asked > 0 {
		var zero T
		return zero, fleetScriptNetworkRefusal(label, asked)
	}
	return out, err
}

// fleetScriptNetworkRefusal is the error of a fleet-bound call whose scripts
// asked for the network asked times. It names the record and the count and
// nothing the script sent.
func fleetScriptNetworkRefusal(label string, asked int) error {
	return fmt.Errorf("%s: %s is fleet-bound, and a Script or Resolve Domain step asked for the network %d time(s); a fleet-bound chain runs without a network until S3, so remove the network call or the step",
		codeFleetScriptNetworkUnavailable, label, asked)
}

// renderTouchesFleet reports whether a render reaches fleet nodes in any
// state: the revision rendered is a fleet sub, the record is fleet-bound
// live, or it is fleet-bound over the union of every record's live and staged
// facts with the rendered revision's own facts added, so a staged revision
// that gathers a fleet member, a staged member that becomes one, and a file
// over either all count.
func renderTouchesFleet(o renderOpened) bool {
	if o.Entry.Flags.FleetBound {
		return true
	}
	facts := factsOfRecord(o.Rec)
	if facts.Kind == kindSub && isFleetSource(facts.Source) {
		return true
	}
	g := &storeGraph{}
	if o.Index != nil {
		g = newStoreGraph(o.Index.Records, graphUnion)
	}
	g.facts = append(g.facts, facts)
	return g.closure(seedFleet, true)[facts.ID]
}

// recordNoun is how a message names a record of rec's kind.
func recordNoun(rec subscriptionRecord) string {
	if recordKind(rec) == kindCollection {
		return "collection " + quoteLabel(rec.ID)
	}
	return "subscription " + quoteLabel(rec.ID)
}
