package secretmask

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestEmbeddedFieldsMatchEncodingJSON(t *testing.T) {
	type Fields struct {
		Name  string
		Count int `json:"count,omitempty"`
	}
	type Other struct{ Name, Extra string }
	type Third struct{ Name string }
	type Tagged struct {
		Value string `json:"Name,omitempty"`
	}
	type AlsoTagged struct {
		Value string `json:"Name"`
	}
	type OptionsOnly struct {
		Name string `json:",omitempty"`
	}
	type Deep struct{ Fields }
	type DeepTagged struct{ Tagged }
	type DeepThird struct{ Third }
	type Left struct{ Fields }
	type Right struct{ Fields }
	type Recursive struct {
		*Recursive
		Name string
	}
	type hidden struct{ Visible string }
	type Label string
	type hiddenLabel string
	type Payload interface{}

	fields := Fields{Name: "embedded", Count: 3}
	other := Other{Name: "other", Extra: "kept"}
	tagged := Tagged{Value: "tagged"}
	tests := []struct {
		name string
		data any
	}{
		{"value", struct {
			Fields
			ID int
		}{fields, 42}},
		{"pointer", struct{ *Fields }{&fields}},
		{"nil pointer", struct{ *Fields }{}},
		{"nested", struct{ Deep }{Deep{fields}}},
		{"named tag", struct {
			Fields `json:"group"`
		}{fields}},
		{"named nil pointer", struct {
			*Fields `json:"group"`
		}{}},
		{"omitted named nil pointer", struct {
			*Fields `json:"group,omitempty"`
		}{}},
		{"options keep promotion", struct {
			Fields `json:",omitempty"`
		}{}},
		{"ignored", struct {
			Fields `json:"-"`
		}{fields}},
		{"literal dash", struct {
			Fields `json:"-,"`
		}{fields}},
		{"invalid name keeps promotion", struct {
			Fields `json:"bad\\name"` //nolint:staticcheck // Deliberately invalid name to verify promotion.
		}{fields}},
		{"unexported struct", struct{ hidden }{hidden{"visible"}}},
		{"unexported pointer", struct{ *hidden }{&hidden{"visible"}}},
		{"nil unexported pointer", struct{ *hidden }{}},
		{"named unexported struct", struct {
			hidden `json:"group"`
		}{hidden{"visible"}}},
		{"nonembedded unexported struct", struct{ value hidden }{hidden{"hidden"}}},
		{"scalar", struct{ Label }{"label"}},
		{"unexported scalar", struct{ hiddenLabel }{"hidden"}},
		{"interface", struct{ Payload }{fields}},
		{"nil interface", struct{ Payload }{}},
		{"outer wins", struct {
			Fields
			Name string
		}{fields, "outer"}},
		{"outer wins before embedded", struct {
			Name string
			Fields
		}{"outer", fields}},
		{"shallow wins over deeper tag", struct {
			DeepTagged
			Name string
		}{DeepTagged{tagged}, "outer"}},
		{"ambiguous", struct {
			Fields
			Other
		}{fields, other}},
		{"tag wins", struct {
			Fields
			Tagged
		}{fields, tagged}},
		{"tag wins before untagged", struct {
			Tagged
			Fields
		}{tagged, fields}},
		{"tag wins over multiple untagged", struct {
			Fields
			Other
			Tagged
		}{fields, other, tagged}},
		{"multiple tagged fields hide untagged", struct {
			Fields
			*Tagged
			*AlsoTagged
		}{fields, &tagged, &AlsoTagged{Value: "also tagged"}}},
		{"ambiguous tags", struct {
			*Tagged
			*AlsoTagged
		}{&tagged, &AlsoTagged{Value: "also tagged"}}},
		{"empty tag is not dominant", struct {
			Fields
			OptionsOnly
		}{fields, OptionsOnly{Name: "other"}}},
		{"omitted winner has no fallback", struct {
			Fields
			Tagged
		}{Fields: fields}},
		{"nil winner has no fallback", struct {
			Fields
			*Tagged
		}{Fields: fields}},
		{"nil pointer still conflicts", struct {
			*Fields
			Other
		}{Other: other}},
		{"ambiguity hides deeper field", struct {
			Fields
			Other
			DeepThird
		}{fields, other, DeepThird{Third{Name: "deeper"}}}},
		{"repeated embedding paths", struct {
			Left
			Right //nolint:govet // Repeated embedding deliberately creates conflicting JSON names.
		}{Left{fields}, Right{Fields{Name: "other"}}}},
		{"nil repeated embedding path", struct {
			*Left
			Right
		}{Right: Right{fields}}},
		{"recursive nil", Recursive{Name: "outer"}},
		{"recursive nonnil", Recursive{Recursive: &Recursive{Name: "inner"}, Name: "outer"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := jsonTree(t, tt.data)
			sm := New()
			for name, transform := range map[string]func(any) any{"Plain": sm.Plain, "Mask": sm.Mask} {
				if got := jsonTree(t, transform(tt.data)); !reflect.DeepEqual(got, want) {
					t.Errorf("%s = %#v, encoding/json = %#v", name, got, want)
				}
			}
		})
	}
}

type hiddenMarshaler struct{}

func (hiddenMarshaler) MarshalJSON() ([]byte, error) {
	return nil, errors.New("hidden field must not be marshaled")
}

func TestHiddenEmbeddedFieldIsNotMarshaled(t *testing.T) {
	type Fields struct{ Value hiddenMarshaler }
	data := struct {
		Fields
		Value string
	}{Value: "outer"}
	want := jsonTree(t, data)
	sm := New()
	for name, transform := range map[string]func(any) any{"Plain": sm.Plain, "Mask": sm.Mask} {
		if got := jsonTree(t, transform(data)); !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %#v, encoding/json = %#v", name, got, want)
		}
	}
}

func TestEmbeddedSecretsAreMaskedAfterFieldSelection(t *testing.T) {
	type Credentials struct {
		Password string `json:"password"`
		Token    string `json:"token,omitempty"`
		Name     string `json:"name"`
	}
	type Override struct {
		Value string `json:"password"`
	}
	credentials := Credentials{Password: "password-secret", Token: "token-secret", Name: "kept"}
	tests := []struct {
		name string
		data any
		want string
	}{
		{"promoted", struct{ Credentials }{credentials}, `{"password":"***","token":"***","name":"kept"}`},
		{"pointer", struct{ *Credentials }{&credentials}, `{"password":"***","token":"***","name":"kept"}`},
		{"named", struct {
			Credentials `json:"nested"`
		}{credentials}, `{"nested":{"password":"***","token":"***","name":"kept"}}`},
		{"secret embedding name", struct {
			Credentials `json:"api_key"`
		}{credentials}, `{"api_key":"***"}`},
		{"nil pointer", struct{ *Credentials }{}, `{}`},
		{"omitted secret", struct{ Credentials }{Credentials{Name: "kept"}}, `{"password":"***","name":"kept"}`},
		{"ambiguous secret", struct {
			*Credentials
			*Override
		}{&credentials, &Override{Value: "override-secret"}}, `{"token":"***","name":"kept"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := New()
			var want any
			if err := json.Unmarshal([]byte(tt.want), &want); err != nil {
				t.Fatal(err)
			}
			if got := jsonTree(t, sm.Mask(tt.data)); !reflect.DeepEqual(got, want) {
				t.Errorf("Mask = %#v, want %#v", got, want)
			}
			if got, want := jsonTree(t, sm.Plain(tt.data)), jsonTree(t, tt.data); !reflect.DeepEqual(got, want) {
				t.Errorf("Plain = %#v, encoding/json = %#v", got, want)
			}
		})
	}
}
