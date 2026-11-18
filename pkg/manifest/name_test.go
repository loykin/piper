package manifest

import "testing"

func TestValidateWorkloadName(t *testing.T) {
	for _, name := range []string{"qa-nb", "a", "nb2", "a-b-c"} {
		if err := ValidateWorkloadName(name); err != nil {
			t.Errorf("%q: unexpected error %v", name, err)
		}
	}
	for _, name := range []string{"", "My Notebook!", "My-Notebook", "-nb", "nb-", "nb_1", "nb.1", string(make([]byte, 64))} {
		if err := ValidateWorkloadName(name); err == nil {
			t.Errorf("%q: expected an error", name)
		}
	}
}
