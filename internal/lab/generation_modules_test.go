package lab

import "testing"

func TestGenerationModuleExamplesMatchValidators(t *testing.T) {
	site := validateSite(object(generationModule("site")["schema"]))
	if len(array(site["files"])) == 0 {
		t.Fatal("site guide must demonstrate a valid local entry file")
	}
	scenario := validateScenario(object(generationModule("scenario")["schema"]))
	rule := object(array(scenario["rules"])[0])
	if !boolean(rule["delivery_required"]) || object(rule["response"])["prompt_prefix"] != "# " {
		t.Fatal("scenario guide must preserve its declared prompt delivery example")
	}
	branches := validateScenario(object(generationModule("scenario")["parameter_example"]))
	if len(array(branches["rules"])) != 3 {
		t.Fatal("parameter guide must demonstrate ordinary files and a delivery branch")
	}
}
