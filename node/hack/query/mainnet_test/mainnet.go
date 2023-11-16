// This tool can be used to send a simple CCQ query request in mainnet.
//
// It requires the following two files:
// ./mainnet_test.nodeKey - This file contains the P2P peer ID to be used. If the file does not exist, it will be created.
// ./mainnet_test.signerKey - This file contains the key used to sign the request. It must exist.
//
// If the nodeKey file does not exist, it will be generated. The log output will print the peerID in the "Test started" line.
// That peerID must be included in the `ccqAllowedPeers` parameter on the guardian.
//
// The signerKey file can be generated by doing: guardiand keygen --block-type "CCQ SERVER SIGNING KEY" /path/to/key/file
// The generated key (which is listed as the `PublicKey` in the file) must be included in the `ccqAllowedRequesters` parameter on the guardian.
//
// To run this tool, do `go run mainnet.go`
//
// - Look for the line saying "Signing key loaded" and confirm the public key matches what is configured on the guardian.
// - Look for the "Test started" and confirm that the peer ID matches what is configured on the guardian.
// - You should see a line saying "Waiting for peers". If you do not, then the test is unable to bootstrap with any guardians.
// - After a few minutes, you should see a message saying "Got peers". If you do not, then test is unable to communicate with any guardians.
// - After this, the test runs, and you should eventually see "Success! Test passed"
//
// To run the tool as a docker image, you can do something like this:
// - wormhole$ docker build --target build -f node/hack/query/mainnet_test/Dockerfile -t mainnet-test .
// - wormhole$ docker run -v /mainnet_test/cfg:/app/cfg mainnet-test /mainnet_test --configDir /app/cfg
// Where /mainnet_test is a directory containing these files:
// - mainnet_test.nodeKey
// - mainnet_test.signerKey

package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/certusone/wormhole/node/hack/query/utils"
	"github.com/certusone/wormhole/node/pkg/common"
	"github.com/certusone/wormhole/node/pkg/p2p"
	gossipv1 "github.com/certusone/wormhole/node/pkg/proto/gossip/v1"
	"github.com/certusone/wormhole/node/pkg/query"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common/hexutil"
	ethCrypto "github.com/ethereum/go-ethereum/crypto"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/tendermint/tendermint/libs/rand"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
)

var (
	p2pNetworkID = flag.String("network", "/wormhole/mainnet/2", "P2P network identifier")
	p2pPort      = flag.Int("port", 8998, "P2P UDP listener port")
	p2pBootstrap = flag.String("bootstrap",
		"/dns4/wormhole-mainnet-v2-bootstrap.certus.one/udp/8996/quic/p2p/12D3KooWQp644DK27fd3d4Km3jr7gHiuJJ5ZGmy8hH4py7fP4FP7,/dns4/wormhole-v2-mainnet-bootstrap.xlabs.xyz/udp/8996/quic/p2p/12D3KooWNQ9tVrcb64tw6bNs2CaNrUGPM7yRrKvBBheQ5yCyPHKC,/dns4/wormhole.mcf.rocks/udp/8996/quic/p2p/12D3KooWDZVv7BhZ8yFLkarNdaSWaB43D6UbQwExJ8nnGAEmfHcU,/dns4/wormhole-v2-mainnet-bootstrap.staking.fund/udp/8996/quic/p2p/12D3KooWG8obDX9DNi1KUwZNu9xkGwfKqTp2GFwuuHpWZ3nQruS1",
		"P2P bootstrap peers (comma-separated)")
	nodeKeyPath   = flag.String("nodeKey", "mainnet_test.nodeKey", "Path to node key (will be generated if it doesn't exist)")
	signerKeyPath = flag.String("signerKey", "mainnet_test.signerKey", "Path to key used to sign unsigned queries")
	configDir     = flag.String("configDir", ".", "Directory where nodeKey and signerKey are loaded from (default is .)")
	targetPeerId  = flag.String("targetPeerId", "", "Only process responses from this peer ID (default is everything)")
)

func main() {

	//
	// BEGIN SETUP
	//

	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger, _ := zap.NewDevelopment()

	nodeKey := *configDir + "/" + *nodeKeyPath
	signerKey := *configDir + "/" + *signerKeyPath

	logger.Info("Loading signing key", zap.String("signingKeyPath", signerKey))
	sk, err := common.LoadArmoredKey(signerKey, CCQ_SERVER_SIGNING_KEY, true)
	if err != nil {
		logger.Fatal("failed to load guardian key", zap.Error(err))
	}
	logger.Info("Signing key loaded", zap.String("publicKey", ethCrypto.PubkeyToAddress(sk.PublicKey).Hex()))

	// Load p2p private key
	var priv crypto.PrivKey
	priv, err = common.GetOrCreateNodeKey(logger, nodeKey)
	if err != nil {
		logger.Fatal("Failed to load node key", zap.Error(err))
	}

	// Manual p2p setup
	components := p2p.DefaultComponents()
	components.Port = uint(*p2pPort)
	bootstrapPeers := *p2pBootstrap
	networkID := *p2pNetworkID + "/ccq"

	h, err := p2p.NewHost(logger, ctx, networkID, bootstrapPeers, components, priv)
	if err != nil {
		panic(err)
	}

	topic_req := fmt.Sprintf("%s/%s", networkID, "ccq_req")
	topic_resp := fmt.Sprintf("%s/%s", networkID, "ccq_resp")

	logger.Info("Subscribing pubsub topic", zap.String("topic_req", topic_req), zap.String("topic_resp", topic_resp))
	ps, err := pubsub.NewGossipSub(ctx, h)
	if err != nil {
		panic(err)
	}

	th_req, err := ps.Join(topic_req)
	if err != nil {
		logger.Panic("failed to join request topic", zap.String("topic_req", topic_req), zap.Error(err))
	}

	th_resp, err := ps.Join(topic_resp)
	if err != nil {
		logger.Panic("failed to join response topic", zap.String("topic_resp", topic_resp), zap.Error(err))
	}

	sub, err := th_resp.Subscribe()
	if err != nil {
		logger.Panic("failed to subscribe to response topic", zap.Error(err))
	}

	logger.Info("Test started", zap.String("peer_id", h.ID().String()),
		zap.String("addrs", fmt.Sprintf("%v", h.Addrs())))

	// Wait for peers
	logger.Info("Waiting for peers")
	for len(th_req.ListPeers()) < 1 {
		time.Sleep(time.Millisecond * 100)
	}
	logger.Info("Got peers", zap.Int("numPeers", len(th_req.ListPeers())))

	// Handle SIGTERM
	sigterm := make(chan os.Signal, 1)
	signal.Notify(sigterm, syscall.SIGTERM)
	signal.Notify(sigterm, syscall.SIGINT)
	go func() {
		<-sigterm
		logger.Info("Received sigterm. exiting.")
		cancel()
	}()

	//
	// END SETUP
	//

	wethAbi, err := abi.JSON(strings.NewReader("[{\"constant\":true,\"inputs\":[],\"name\":\"name\",\"outputs\":[{\"name\":\"\",\"type\":\"string\"}],\"payable\":false,\"stateMutability\":\"view\",\"type\":\"function\"},{\"constant\":true,\"inputs\":[],\"name\":\"totalSupply\",\"outputs\":[{\"name\":\"\",\"type\":\"uint256\"}],\"payable\":false,\"stateMutability\":\"view\",\"type\":\"function\"}]"))
	if err != nil {
		panic(err)
	}

	methods := []string{"name", "totalSupply"}
	callData := []*query.EthCallData{}
	to, _ := hex.DecodeString("C02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2")

	for _, method := range methods {
		data, err := wethAbi.Pack(method)
		if err != nil {
			panic(err)
		}

		callData = append(callData, &query.EthCallData{
			To:   to,
			Data: data,
		})
	}

	// Fetch the latest block number
	//url := "https://localhost:8545"
	url := "https://rpc.ankr.com/eth"
	logger.Info("Querying for latest block height", zap.String("url", url))
	blockNum, err := utils.FetchLatestBlockNumberFromUrl(ctx, url)
	if err != nil {
		logger.Fatal("Failed to fetch latest block number", zap.Error(err))
	}

	logger.Info("latest block", zap.String("num", blockNum.String()), zap.String("encoded", hexutil.EncodeBig(blockNum)))

	// block := "0x28d9630"
	// block := "latest"
	// block := "0x9999bac44d09a7f69ee7941819b0a19c59ccb1969640cc513be09ef95ed2d8e2"

	// Start of query creation...
	callRequest := &query.EthCallQueryRequest{
		BlockId:  hexutil.EncodeBig(blockNum),
		CallData: callData,
	}

	// Send 2 individual requests for the same thing but 5 blocks apart
	// First request...
	logger.Info("calling sendQueryAndGetRsp for ", zap.String("blockNum", blockNum.String()), zap.String("publicKey", ethCrypto.PubkeyToAddress(sk.PublicKey).Hex()))
	queryRequest := createQueryRequest(callRequest)
	sendQueryAndGetRsp(queryRequest, sk, th_req, ctx, logger, sub, wethAbi, methods)

	// This is just so that when I look at the output, it is easier for me. (Paul)
	logger.Info("sleeping for 5 seconds")
	time.Sleep(time.Second * 5)

	// Cleanly shutdown
	// Without this the same host won't properly discover peers until some timeout
	sub.Cancel()
	if err := th_req.Close(); err != nil {
		logger.Fatal("Error closing the request topic", zap.Error(err))
	}
	if err := th_resp.Close(); err != nil {
		logger.Fatal("Error closing the response topic", zap.Error(err))
	}
	if err := h.Close(); err != nil {
		logger.Fatal("Error closing the host", zap.Error(err))
	}

	//
	// END SHUTDOWN
	//

	logger.Info("Success! Test passed!")
}

const (
	CCQ_SERVER_SIGNING_KEY = "CCQ SERVER SIGNING KEY"
)

func createQueryRequest(callRequest *query.EthCallQueryRequest) *query.QueryRequest {
	queryRequest := &query.QueryRequest{
		Nonce: rand.Uint32(),
		PerChainQueries: []*query.PerChainQueryRequest{
			{
				ChainId: 2,
				Query:   callRequest,
			},
		},
	}
	return queryRequest
}

func createQueryRequestWithMultipleRequests(callRequests []*query.EthCallQueryRequest) *query.QueryRequest {
	perChainQueries := []*query.PerChainQueryRequest{}
	for _, req := range callRequests {
		perChainQueries = append(perChainQueries, &query.PerChainQueryRequest{
			ChainId: 2,
			Query:   req,
		})
	}

	queryRequest := &query.QueryRequest{
		Nonce:           rand.Uint32(),
		PerChainQueries: perChainQueries,
	}
	return queryRequest
}

func sendQueryAndGetRsp(queryRequest *query.QueryRequest, sk *ecdsa.PrivateKey, th *pubsub.Topic, ctx context.Context, logger *zap.Logger, sub *pubsub.Subscription, wethAbi abi.ABI, methods []string) {
	queryRequestBytes, err := queryRequest.Marshal()
	if err != nil {
		panic(err)
	}
	numQueries := len(queryRequest.PerChainQueries)

	// Sign the query request using our private key.
	digest := query.QueryRequestDigest(common.MainNet, queryRequestBytes)
	sig, err := ethCrypto.Sign(digest.Bytes(), sk)
	if err != nil {
		panic(err)
	}

	signedQueryRequest := &gossipv1.SignedQueryRequest{
		QueryRequest: queryRequestBytes,
		Signature:    sig,
	}

	msg := gossipv1.GossipMessage{
		Message: &gossipv1.GossipMessage_SignedQueryRequest{
			SignedQueryRequest: signedQueryRequest,
		},
	}

	b, err := proto.Marshal(&msg)
	if err != nil {
		panic(err)
	}

	err = th.Publish(ctx, b)
	if err != nil {
		panic(err)
	}

	logger.Info("Waiting for message...")
	// TODO: max wait time
	// TODO: accumulate signatures to reach quorum
	for {
		envelope, err := sub.Next(ctx)
		if err != nil {
			logger.Panic("failed to receive pubsub message", zap.Error(err))
		}
		var msg gossipv1.GossipMessage
		err = proto.Unmarshal(envelope.Data, &msg)
		if err != nil {
			logger.Info("received invalid message",
				zap.Binary("data", envelope.Data),
				zap.String("from", envelope.GetFrom().String()))
			continue
		}
		var isMatchingResponse bool
		switch m := msg.Message.(type) {
		case *gossipv1.GossipMessage_SignedQueryResponse:
			if *targetPeerId != "" && envelope.GetFrom().String() != *targetPeerId {
				continue
			}
			logger.Info("query response received",
				zap.String("from", envelope.GetFrom().String()),
				zap.Any("response", m.SignedQueryResponse),
				zap.String("responseBytes", hexutil.Encode(m.SignedQueryResponse.QueryResponse)),
				zap.String("sigBytes", hexutil.Encode(m.SignedQueryResponse.Signature)))
			var response query.QueryResponsePublication
			err := response.Unmarshal(m.SignedQueryResponse.QueryResponse)
			if err != nil {
				logger.Warn("failed to unmarshal response", zap.Error(err))
				break
			}
			if bytes.Equal(response.Request.QueryRequest, queryRequestBytes) && bytes.Equal(response.Request.Signature, sig) {
				// TODO: verify response signature
				isMatchingResponse = true

				if len(response.PerChainResponses) != numQueries {
					logger.Warn("unexpected number of per chain query responses", zap.Int("expectedNum", numQueries), zap.Int("actualNum", len(response.PerChainResponses)))
					break
				}
				// Do double loop over responses
				for index := range response.PerChainResponses {
					logger.Info("per chain query response index", zap.Int("index", index))

					var localCallData []*query.EthCallData
					switch ecq := queryRequest.PerChainQueries[index].Query.(type) {
					case *query.EthCallQueryRequest:
						localCallData = ecq.CallData
					default:
						panic("unsupported query type")
					}

					var localResp *query.EthCallQueryResponse
					switch ecq := response.PerChainResponses[index].Response.(type) {
					case *query.EthCallQueryResponse:
						localResp = ecq
					default:
						panic("unsupported query type")
					}

					if len(localResp.Results) != len(localCallData) {
						logger.Warn("unexpected number of results", zap.Int("expectedNum", len(localCallData)), zap.Int("expectedNum", len(localResp.Results)))
						break
					}

					for idx, resp := range localResp.Results {
						result, err := wethAbi.Methods[methods[idx]].Outputs.Unpack(resp)
						if err != nil {
							logger.Warn("failed to unpack result", zap.Error(err))
							break
						}

						resultStr := hexutil.Encode(resp)
						logger.Info("found matching response", zap.Int("idx", idx), zap.Uint64("number", localResp.BlockNumber), zap.String("hash", localResp.Hash.String()), zap.String("time", localResp.Time.String()), zap.String("method", methods[idx]), zap.Any("resultDecoded", result), zap.String("resultStr", resultStr))
					}
				}
			}
		default:
			continue
		}
		if isMatchingResponse {
			break
		}
	}
}
