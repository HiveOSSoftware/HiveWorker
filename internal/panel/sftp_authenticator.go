package panel

import "context"

type SFTPAuthenticator interface {
	Authenticate(
		ctx context.Context,
		username string,
		password string,
	) (*SFTPAuthResponse, error)
}
