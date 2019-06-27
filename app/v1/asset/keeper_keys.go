package asset

import (
	"fmt"

	sdk "github.com/irisnet/irishub/types"
)

var (
	PrefixGateway = []byte("gateways:") // prefix for the gateway store
)

// KeyToken returns the key of the specified token source and id
func KeyToken(tokenId string) []byte {
	keyId, _ := sdk.ConvertIdToTokenKeyId(tokenId)
	return []byte(fmt.Sprintf("token:%s", keyId))
}

// KeyGateway returns the key of the specified moniker
func KeyGateway(moniker string) []byte {
	return []byte(fmt.Sprintf("gateways:%s", moniker))
}

// KeyOwnerGateway returns the key of the specifed owner and moniker. Intended for querying all gateways of an owner
func KeyOwnerGateway(owner sdk.AccAddress, moniker string) []byte {
	return []byte(fmt.Sprintf("ownerGateways:%d:%s", owner, moniker))
}

// KeyGatewaysSubspace returns the key prefix for iterating on all gateways of an owner
func KeyGatewaysSubspace(owner sdk.AccAddress) []byte {
	return []byte(fmt.Sprintf("ownerGateways:%d:", owner))
}