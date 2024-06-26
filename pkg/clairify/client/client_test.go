package client

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackrox/scanner/pkg/clairify/fixtures"
	"github.com/stackrox/scanner/pkg/clairify/types"
	"github.com/stretchr/testify/suite"
)

func TestClientSuite(t *testing.T) {
	suite.Run(t, new(ClientTestSuite))
}

type ClientTestSuite struct {
	suite.Suite

	client *Clairify
	server *httptest.Server
}

func (suite *ClientTestSuite) SetupSuite() {
	masterRouter := http.NewServeMux()
	masterRouter.HandleFunc("/scanner/ping", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("{}"))
	})
	masterRouter.HandleFunc("/scanner/image/docker.io/library/nginx/1.10", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(fixtures.GetLayerResponse))
	})
	masterRouter.HandleFunc("/scanner/image/docker.io/library/nginx/badtag", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(fixtures.ErrorResponse))
	})
	masterRouter.HandleFunc("/scanner/sha/goodsha", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(fixtures.GetLayerResponse))
	})
	masterRouter.HandleFunc("/scanner/sha/badsha", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(fixtures.ErrorResponse))
	})
	masterRouter.HandleFunc("/scanner/image", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Basic") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(fixtures.GetImageResponse))
	})
	masterRouter.HandleFunc("/scanner/vulndefs/metadata", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(fixtures.GetVulnDefsMetadata))
	})

	suite.server = httptest.NewServer(masterRouter)
	suite.client = New(suite.server.URL, true)
}

func (suite *ClientTestSuite) TearDownSuite() {
	suite.server.Close()
}

func (suite *ClientTestSuite) TestPing() {
	suite.NoError(suite.client.Ping())
}

func (suite *ClientTestSuite) TestAddImage() {
	imageRequest := &types.ImageRequest{
		Image:    "image",
		Registry: "registry",
	}
	expectedImage := &types.Image{
		SHA:      "sha",
		Registry: "registry",
		Remote:   "namespace/repo",
		Tag:      "tag",
	}
	image, err := suite.client.AddImage("username", "password", imageRequest)
	suite.NoError(err)
	suite.Equal(expectedImage, image)
}

func (suite *ClientTestSuite) TestRetrieveImageDataBySHA() {
	image := &types.Image{
		Registry: "docker.io",
		Remote:   "library/nginx",
		Tag:      "1.10",
	}
	envelope, err := suite.client.RetrieveImageDataByName(image, nil)
	suite.NoError(err)
	suite.NotNil(envelope)

	image.Tag = "badtag"
	_, err = suite.client.RetrieveImageDataByName(image, nil)
	suite.Error(err)
}

func (suite *ClientTestSuite) TestRetrieveImageDataByName() {
	envelope, err := suite.client.RetrieveImageDataBySHA("goodsha", nil)
	suite.NoError(err)
	suite.NotNil(envelope)

	_, err = suite.client.RetrieveImageDataBySHA("badsha", nil)
	suite.Error(err)
}

func (suite *ClientTestSuite) TestGetVulnDefsMetadata() {
	info, err := suite.client.GetVulnDefsMetadata()
	suite.NoError(err)
	suite.NotNil(info)
}
