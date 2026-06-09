package telegram

import (
	"testing"
)

func TestSendTestMsg(t *testing.T) {
	mySender := NewSender("ff")               // your bot token here
	err := mySender.SendMsg(12345678, "test") // your chat id here
	if err != nil {
		t.Fail()
	}
	t.Logf("done")
}
