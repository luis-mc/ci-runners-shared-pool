package runners

import "testing"

func TestValidateLabelsAcceptsSizeLabelsAndRefusesBadOnes(t *testing.T) {
	if _, err := ValidateLabels([]string{"self-hosted", "cpu=4", "ram=8"}, "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateLabels([]string{"cpu=0"}, "", nil); err == nil {
		t.Fatal("cpu=0 must be refused")
	}
}
