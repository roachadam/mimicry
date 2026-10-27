package server

// Planned is a scenario a refresh captures (cmd/refresh), with its number
// of samples.
type Planned struct {
	Scenario *Scenario
	Samples  int
}

// QuickScenarios are the fingerprint layer, what a quick refresh recaptures
// for every new browser version: the TLS hello, the HTTP/2 preface and
// SETTINGS and the navigation's headers (the default page), the
// navigation over HTTP/1.1, every request kind's headers over HTTP/2,
// HTTP/1.1 and plain HTTP, the WebSocket handshakes, and the QUIC hello
// and HTTP/3 SETTINGS. Three samples where a browser picks something per
// connection (the TLS and QUIC hellos: extension order, GREASE, ECH
// lengths), one elsewhere. Every other scenario's captures are inherited
// from the browser's previous version through the profile's captures_from.
var QuickScenarios = []Planned{
	{
		Scenario: DefaultScenario,
		Samples:  3,
	},
	{
		Scenario: scenarioNamed("http1"),
		Samples:  1,
	},
	{
		Scenario: Kinds,
		Samples:  1,
	},
	{
		Scenario: KindsHTTP1,
		Samples:  1,
	},
	{
		Scenario: KindsPlain,
		Samples:  1,
	},
	{
		Scenario: WebSocket,
		Samples:  1,
	},
	{
		Scenario: WebSocketH2,
		Samples:  1,
	},
	{
		Scenario: H3Discovery,
		Samples:  3,
	},
	{
		Scenario: H3Page,
		Samples:  3,
	},
}

// FullScenarios are every scenario, three samples each: a full refresh,
// before a release or when a quick one shows a change beyond version
// strings. Replays (not in Scenarios) are the refresh tool's to add.
func FullScenarios() []Planned {
	out := make([]Planned, len(Scenarios))
	for i, sc := range Scenarios {
		out[i] = Planned{
			Scenario: sc,
			Samples:  3,
		}
	}
	return out
}

// scenarioNamed is ScenarioByName for a name Scenarios is known to hold.
func scenarioNamed(name string) *Scenario {
	sc, err := ScenarioByName(name)
	if err != nil {
		panic(err)
	}
	return sc
}
