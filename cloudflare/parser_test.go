package cloudflare

import (
	"errors"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParseCIDRTextNormalizesIPv4WhitespaceAndDuplicates(t *testing.T) {
	t.Parallel()
	text := "  203.0.113.99/24  \r\n\r\n198.51.100.0/24\r\n203.0.113.0/24\r\n"
	rangeSet, err := ParseCIDRText(text, EndpointFamilyIPv4, time.Time{})
	if err != nil {
		t.Fatalf("ParseCIDRText() error = %v", err)
	}
	want := []netip.Prefix{
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
	}
	if !slices.Equal(rangeSet.Prefixes(), want) {
		t.Fatalf("Prefixes() = %v, want %v", rangeSet.Prefixes(), want)
	}
}

func TestParseCIDRTextNormalizesIPv6(t *testing.T) {
	t.Parallel()
	rangeSet, err := ParseCIDRText("2001:db8:2::1/48\n2001:db8:1::/48\n", EndpointFamilyIPv6, time.Time{})
	if err != nil {
		t.Fatalf("ParseCIDRText() error = %v", err)
	}
	want := []netip.Prefix{
		netip.MustParsePrefix("2001:db8:1::/48"),
		netip.MustParsePrefix("2001:db8:2::/48"),
	}
	if !slices.Equal(rangeSet.Prefixes(), want) {
		t.Fatalf("Prefixes() = %v, want %v", rangeSet.Prefixes(), want)
	}
}

func TestParseCIDRTextRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		text   string
		family EndpointFamily
		limit  int
		want   error
	}{
		{name: "非法 CIDR", text: "not-a-cidr", family: EndpointFamilyIPv4, limit: 2, want: ErrInvalidCIDR},
		{name: "IPv4 端点混入 IPv6", text: "2001:db8::/32", family: EndpointFamilyIPv4, limit: 2, want: ErrWrongAddressFamily},
		{name: "IPv6 端点混入 IPv4", text: "192.0.2.0/24", family: EndpointFamilyIPv6, limit: 2, want: ErrWrongAddressFamily},
		{name: "IPv6 端点混入映射地址", text: "::ffff:192.0.2.0/120", family: EndpointFamilyIPv6, limit: 2, want: ErrWrongAddressFamily},
		{name: "空集合", text: " \r\n\t\n", family: EndpointFamilyIPv4, limit: 2, want: ErrEmptyRangeSet},
		{name: "超限", text: "192.0.2.0/24\n198.51.100.0/24\n203.0.113.0/24", family: EndpointFamilyIPv4, limit: 2, want: ErrTooManyPrefixes},
		{name: "非法地址族", text: "192.0.2.0/24", family: 5, limit: 2, want: ErrInvalidEndpointFamily},
		{name: "非法上限", text: "192.0.2.0/24", family: EndpointFamilyIPv4, limit: 0, want: ErrInvalidPrefixLimit},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseCIDRTextWithLimit(test.text, test.family, time.Time{}, test.limit)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want errors.Is(_, %v)", err, test.want)
			}
		})
	}
}

func TestParseCIDRTextDefaultLimit(t *testing.T) {
	t.Parallel()
	lines := make([]string, DefaultMaxPrefixes+1)
	for i := range lines {
		lines[i] = "192.0.2.0/24"
	}
	_, err := ParseCIDRText(strings.Join(lines, "\n"), EndpointFamilyIPv4, time.Time{})
	if !errors.Is(err, ErrTooManyPrefixes) {
		t.Fatalf("error = %v, want %v", err, ErrTooManyPrefixes)
	}
}
