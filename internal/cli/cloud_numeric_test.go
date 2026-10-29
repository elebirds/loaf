package cli

import "testing"

func TestParseCloudIntegerNormalizesHumanInput(t *testing.T) {
    for _, tc := range []struct {
        input string
        want  int
    }{
        {"0", 0}, {"1", 1}, {"64", 64}, {" 1 ", 1}, {"\t1\u00a0", 1},
        {"１", 1}, {"　１２　", 12}, {"６４", 64}, {"-1", -1},
    } {
        got, err := parseCloudInteger(tc.input)
        if err != nil || got != tc.want {
            t.Fatalf("%q: got %d, %v; want %d", tc.input, got, err, tc.want)
        }
    }
    for _, value := range []string{"", " ", "1.0", "１.０", "1 2", "NaN", "1e3", "1,000", "92233720368547758080"} {
        if _, err := parseCloudInteger(value); err == nil {
            t.Fatalf("accepted invalid integer %q", value)
        }
    }
}
