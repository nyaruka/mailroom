package handlers_test

import (
	"testing"

	"github.com/nyaruka/mailroom/testsuite"
	"github.com/nyaruka/vkutil/assertvk"
)

func TestWarning(t *testing.T) {
	_, rt := testsuite.Runtime(t)

	defer testsuite.Reset(t, rt, testsuite.ResetValkey|testsuite.ResetDynamo|testsuite.ResetData)

	runTests(t, rt, "testdata/warning.json")

	vc := rt.VK.Get()
	defer vc.Close()

	assertvk.HGetAll(t, vc, "deprecated_context_usage", map[string]string{
		"9de3663f-c5c5-4c92-9f45-ecbc09abcc85/contact.id":   "2",
		"9de3663f-c5c5-4c92-9f45-ecbc09abcc85/legacy_extra": "1",
	})
}
