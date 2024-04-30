package auth

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Azure/go-autorest/autorest"
	"github.com/Azure/go-autorest/autorest/adal"
	"github.com/Azure/go-autorest/autorest/azure"
	"github.com/golang-jwt/jwt/v5"
	"github.com/lestrrat-go/jwx/jwa"
	"github.com/lestrrat-go/jwx/jwk"
)

const (
	MicrosoftEntraIDCommonPublicKeysEndpoint = "https://login.microsoftonline.com/common/discovery/v2.0/keys"
	TokenType                                = "Bearer"
)

type OAuthTokenGrantFlow int

const (
	ClientCredentials OAuthTokenGrantFlow = iota
	Undefined
)

type ServicePrincipalTokenParameters struct {
	AzureEnvironment azure.Environment
	TenantID         string
	ClientID         string
	ClientSecret     string
	Resource         string
	Token            adal.Token
}

type AccessTokenAzureClaims struct {
	AppID    string `json:"appid"`
	TenantID string `json:"tid"`
	jwt.RegisteredClaims
}

// Get the bearer token authorizer for the respective oauth token grant flow.
func GetBearerTokenAuthorizer(
	azureEnvironment azure.Environment,
	tenantID string,
	clientID string,
	clientSecret string,
	accessToken string,
) (*autorest.BearerAuthorizer, error) {
	sptParameters := &ServicePrincipalTokenParameters{
		AzureEnvironment: azureEnvironment,
		TenantID:         tenantID,
		ClientID:         clientID,
		ClientSecret:     clientSecret,
	}

	grantFlow := ClientCredentials

	if len(strings.TrimSpace(accessToken)) != 0 {
		claims, err := verifyAccessToken(accessToken)
		if err != nil {
			return nil, err
		}

		grantFlow = Undefined
		sptParameters.ClientID = claims.AppID
		sptParameters.TenantID = claims.TenantID
		if len(claims.RegisteredClaims.Audience) != 0 {
			sptParameters.Resource = claims.RegisteredClaims.Audience[0]
		}

		notBefore := claims.RegisteredClaims.NotBefore.Unix()
		expiresOn := claims.RegisteredClaims.ExpiresAt.Unix()
		oauthToken := adal.Token{
			AccessToken: accessToken,
			ExpiresIn:   json.Number(fmt.Sprint(expiresOn - notBefore)), // The default access token lifetime - 3599
			ExpiresOn:   json.Number(fmt.Sprint(expiresOn)),
			NotBefore:   json.Number(fmt.Sprint(notBefore)),
			Resource:    sptParameters.Resource,
			Type:        TokenType,
		}

		sptParameters.Token = oauthToken
	}

	spt, err := newServicePrincipalToken(sptParameters, grantFlow)
	if err != nil {
		return nil, fmt.Errorf("error getting service principal token: %s", err)
	}

	return autorest.NewBearerAuthorizer(spt), nil
}

func newServicePrincipalToken(
	sptParameters *ServicePrincipalTokenParameters,
	tokenGrantFlow OAuthTokenGrantFlow,
) (*adal.ServicePrincipalToken, error) {
	// Get a token used for authorizing requests to Azure
	oauthConfig, err := adal.NewOAuthConfig(
		sptParameters.AzureEnvironment.ActiveDirectoryEndpoint,
		sptParameters.TenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("error building oauth config: %s", err)
	}

	switch tokenGrantFlow {
	case ClientCredentials:
		return adal.NewServicePrincipalToken(
			*oauthConfig,
			sptParameters.ClientID,
			sptParameters.ClientSecret,
			sptParameters.AzureEnvironment.ResourceManagerEndpoint,
		)
	default:
		return adal.NewServicePrincipalTokenFromManualToken(
			*oauthConfig,
			sptParameters.ClientID,
			sptParameters.Resource,
			sptParameters.Token,
		)
	}
}

func verifyAccessToken(accessToken string) (*AccessTokenAzureClaims, error) {
	keySet, err := jwk.Fetch(context.Background(), MicrosoftEntraIDCommonPublicKeysEndpoint)

	token, err := jwt.ParseWithClaims(accessToken, &AccessTokenAzureClaims{}, func(token *jwt.Token) (interface{}, error) {
		if token.Method.Alg() != jwa.RS256.String() {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		kid, ok := token.Header["kid"].(string)
		if !ok {
			return nil, fmt.Errorf("the header 'kid' is not found")
		}

		keys, ok := keySet.LookupKeyID(kid)
		if !ok {
			return nil, fmt.Errorf("the key '%v' not found", kid)
		}

		publickey := &rsa.PublicKey{}
		err = keys.Raw(publickey)
		if err != nil {
			return nil, fmt.Errorf("unable to parse the public key")
		}

		return publickey, nil
	})

	if err != nil {
		return nil, err
	} else if !token.Valid {
		return nil, fmt.Errorf("the access token provided is invalid")
	}

	if claims, ok := token.Claims.(*AccessTokenAzureClaims); ok {
		if err := validateClaim(claims.AppID, "AppID"); err != nil {
			return nil, err
		}
		if err := validateClaim(claims.TenantID, "TenantID"); err != nil {
			return nil, err
		}

		return claims, nil
	} else {
		return nil, fmt.Errorf("unknown claims type")
	}
}

func validateClaim(claim, name string) error {
	if len(strings.TrimSpace(claim)) == 0 {
		return fmt.Errorf("the claim '%s' is not found in the access token", name)
	}

	return nil
}
