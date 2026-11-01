package server

import (
	"testing"
)

// TestIOSListsNameScenarios keeps the iPhone's scenario lists to scenarios
// that exist.
func TestIOSListsNameScenarios(t *testing.T) {
	for _, name := range append(append([]string{}, iosCORSOrder...), iosAutomationMatches...) {
		if _, err := ScenarioByName(name); err != nil {
			t.Error(err)
		}
	}

	if Redirects2.AutomationDiffersOnIOS() == "" || Kinds.AutomationDiffersOnIOS() == "" || DefaultScenario.AutomationDiffersOnIOS() != "" {
		t.Errorf("AutomationDiffersOnIOS: redirects2 %q, kinds %q, page %q", Redirects2.AutomationDiffersOnIOS(), Kinds.AutomationDiffersOnIOS(), DefaultScenario.AutomationDiffersOnIOS())
	}
}
