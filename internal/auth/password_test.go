package auth

import "testing"

func TestCheckPolicy(t *testing.T) {
	cases := []struct {
		in string
		ok bool
	}{
		{"short", false},
		{"nouppercase1!", false},
		{"NOLOWERCASE1!", false},
		{"NoDigit!!!!!", false},
		{"NoSymbol12345", false},
		{"GoodPassw0rd!", true},
	}
	for _, c := range cases {
		err := CheckPolicy(c.in)
		if c.ok && err != nil {
			t.Fatalf("%q 应通过：%v", c.in, err)
		}
		if !c.ok && err == nil {
			t.Fatalf("%q 应拒绝", c.in)
		}
	}
}

func TestHashAndCheck(t *testing.T) {
	h, err := HashPassword("GoodPassw0rd!")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword("GoodPassw0rd!", h) {
		t.Fatal("正确密码未通过")
	}
	if CheckPassword("WrongPassw0rd!", h) {
		t.Fatal("错误密码通过")
	}
}

func TestRandomPassword(t *testing.T) {
	for i := 0; i < 20; i++ {
		p, err := RandomPassword()
		if err != nil {
			t.Fatal(err)
		}
		if err := CheckPolicy(p); err != nil {
			t.Fatalf("随机密码 %q 不符合策略：%v", p, err)
		}
	}
}
