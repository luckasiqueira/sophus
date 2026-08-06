package middlewares

import "testing"

func TestMediaCompanyID(t *testing.T) {
	tests := []struct {
		path    string
		want    int
		wantErr bool
	}{
		{path: "/medias/12/image.jpg", want: 12},
		{path: "/medias/not-a-company/image.jpg", wantErr: true},
		{path: "/medias/12", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			got, err := mediaCompanyID(test.path)
			if (err != nil) != test.wantErr {
				t.Fatalf("mediaCompanyID(%q) error = %v, wantErr %v", test.path, err, test.wantErr)
			}
			if got != test.want {
				t.Fatalf("mediaCompanyID(%q) = %d, want %d", test.path, got, test.want)
			}
		})
	}
}
