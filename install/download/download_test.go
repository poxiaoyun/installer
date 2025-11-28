package download

import "testing"

func TestPerRepoCacheDir(t *testing.T) {
	tests := []struct {
		repo    string
		basedir string
		want    string
	}{
		{
			repo:    "https://foo.com/bar",
			basedir: "/app/plugins",
			want:    "/app/plugins/foo.com/bar",
		},
	}
	for _, tt := range tests {
		t.Run(tt.repo, func(t *testing.T) {
			if got := PerRepoCacheDir(tt.repo, tt.basedir); got != tt.want {
				t.Errorf("PerRepoCacheDir() = %v, want %v", got, tt.want)
			}
		})
	}
}
