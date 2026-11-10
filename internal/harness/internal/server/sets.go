package server

// Planned is a scenario a refresh captures (cmd/refresh), with its number
// of samples.
type Planned struct {
	Scenario *Scenario
	Samples  int
}

// QuickScenarios are the fingerprint layer a quick refresh recaptures for
// every new browser version: hellos, SETTINGS and request headers per
// transport, and the client hints, whose full version moves with every
// release. Hellos get three samples (they vary per connection); other
// scenarios inherit captures through the profile's capturesFrom.
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
	{
		Scenario: HRR,
		Samples:  3,
	},
	{
		Scenario: ECH,
		Samples:  3,
	},
	{
		Scenario: ECHH3,
		Samples:  3,
	},
	{
		Scenario: ClientHints,
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

// IOSScenarios are what a refresh captures of Safari on an iPhone, three
// samples each (decision 0051): the fingerprint layer, the scenarios
// kindsgen and the rules are derived from, how uploads are cut into TLS
// records (http1-upload, h2-upload), and the QUIC failure pair
// (h3-blackhole, h3-broken: the fallback, loss and keep-alive rules), from
// which the profile inherits every other scenario but those
// NotInheritedOnIOS keeps out. The direct scenarios come first, then the
// proxy ones, for which someone sets the iPhone's Wi-Fi proxy by hand.
func IOSScenarios() []Planned {
	names := []string{
		"page",
		"kinds",
		"kinds-http1",
		"kinds-plain",
		"cookies",
		"cors",
		"cors-edge",
		"cors-edge-cookies",
		"redirects2",
		"redirects2-cookies",
		"headers-order",
		"http1",
		"http1-upload",
		"h2-upload",
		"h3-discovery",
		"h3-page",
		"resume",
		"websocket",
		"websocket-h2",
		"h3-blackhole",
		"h3-broken",
		"proxy-connect",
		"proxy-plain",
	}
	out := make([]Planned, len(names))

	for i, name := range names {
		out[i] = Planned{
			Scenario: scenarioNamed(name),
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
