package oidc

// SetJWKSWaiterAddedForTest records when a provider joins an in-flight JWKS load.
func SetJWKSWaiterAddedForTest(provider *Provider, waiterAdded func()) {
	provider.keySet.waiterAdded = waiterAdded
}
