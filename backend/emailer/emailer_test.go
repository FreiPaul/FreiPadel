package emailer

import (
	"fmt"
	"os"
	"testing"
)

func TestSendEmail(t *testing.T) {
	emailer := NewSender(
		os.Getenv("SMTP_HOST"),
		os.Getenv("SMTP_PORT"),
		os.Getenv("SMTP_USER"),
		os.Getenv("SMTP_PASS"),
		os.Getenv("MAIL_FROM"),
	)

	fmt.Printf("host: %s", os.Getenv("SMTP_HOST"))

	err := emailer.Send("paul.herrmann@rwth-aachen.de", "Test Email", "The test worked!")

	if err != nil {
		t.Fatal("Sending of email did not work:", err)
	}
}
