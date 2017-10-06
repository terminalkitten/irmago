package irmago

import (
	"encoding/json"
	"time"

	"github.com/go-errors/errors"
	"github.com/mhe/gabi"
)

type LogEntry struct {
	// General info
	Type        Action
	Time        Timestamp    // Time at which the session was completed
	SessionInfo *SessionInfo // Message that started the session

	// Session type-specific info
	Disclosed         map[CredentialTypeIdentifier]map[int]TranslatedString // Any session type
	Received          map[CredentialTypeIdentifier][]TranslatedString       // In case of issuance session
	Removed           map[CredentialTypeIdentifier][]TranslatedString       // In case of credential removal
	SignedMessage     []byte                                                // In case of signature sessions
	SignedMessageType string                                                // In case of signature sessions

	response    interface{}     // Our response (ProofList or IssueCommitmentMessage)
	rawResponse json.RawMessage // Unparsed []byte version of response
}

const actionRemoval = Action("removal")

func (session *session) createLogEntry(response interface{}) (*LogEntry, error) {
	entry := &LogEntry{
		Type:        session.Action,
		Time:        Timestamp(time.Now()),
		SessionInfo: session.info,
		response:    response,
	}

	// Populate session type-specific fields of the log entry (except for .Disclosed which is handled below)
	var prooflist gabi.ProofList
	var ok bool
	switch entry.Type {
	case ActionSigning:
		entry.SignedMessage = []byte(session.jwt.(*SignatureRequestorJwt).Request.Request.Message)
		entry.SignedMessageType = session.jwt.(*SignatureRequestorJwt).Request.Request.MessageType
		fallthrough
	case ActionDisclosing:
		if prooflist, ok = response.(gabi.ProofList); !ok {
			return nil, errors.New("Response was not a ProofList")
		}
	case ActionIssuing:
		if entry.Received == nil {
			entry.Received = map[CredentialTypeIdentifier][]TranslatedString{}
		}
		for _, req := range session.jwt.(*IdentityProviderJwt).Request.Request.Credentials {
			list, err := req.AttributeList(session.credManager.ConfigurationStore)
			if err != nil {
				continue // TODO?
			}
			entry.Received[list.CredentialType().Identifier()] = list.Strings()
		}
		var msg *gabi.IssueCommitmentMessage
		if msg, ok = response.(*gabi.IssueCommitmentMessage); ok {
			prooflist = msg.Proofs
		} else {
			return nil, errors.New("Response was not a *IssueCommitmentMessage")
		}
	default:
		return nil, errors.New("Invalid log type")
	}

	// Populate the list of disclosed attributes .Disclosed
	for _, proof := range prooflist {
		if proofd, isproofd := proof.(*gabi.ProofD); isproofd {
			if entry.Disclosed == nil {
				entry.Disclosed = map[CredentialTypeIdentifier]map[int]TranslatedString{}
			}
			meta := MetadataFromInt(proofd.ADisclosed[1], session.credManager.ConfigurationStore)
			id := meta.CredentialType().Identifier()
			entry.Disclosed[id] = map[int]TranslatedString{}
			for i, attr := range proofd.ADisclosed {
				if i == 1 {
					continue
				}
				val := string(attr.Bytes())
				entry.Disclosed[id][i] = TranslatedString{"en": val, "nl": val}
			}
		}
	}

	return entry, nil
}

func (entry *LogEntry) Jwt() (RequestorJwt, string, error) {
	return parseRequestorJwt(entry.Type, entry.SessionInfo.Jwt)
}

func (entry *LogEntry) GetResponse() (interface{}, error) {
	if entry.response == nil {
		switch entry.Type {
		case actionRemoval:
			return nil, nil
		case ActionSigning:
			fallthrough
		case ActionDisclosing:
			entry.response = []*gabi.ProofD{}
		case ActionIssuing:
			entry.response = &gabi.IssueCommitmentMessage{}
		default:
			return nil, errors.New("Invalid log type")
		}
		err := json.Unmarshal(entry.rawResponse, entry.response)
		if err != nil {
			return nil, err
		}
	}

	return entry.response, nil
}

type jsonLogEntry struct {
	Type        Action
	Time        Timestamp
	SessionInfo *logSessionInfo

	Disclosed         map[CredentialTypeIdentifier]map[int]TranslatedString `json:",omitempty"`
	Received          map[CredentialTypeIdentifier][]TranslatedString       `json:",omitempty"`
	Removed           map[CredentialTypeIdentifier][]TranslatedString       `json:",omitempty"`
	SignedMessage     []byte                                                `json:",omitempty"`
	SignedMessageType string                                                `json:",omitempty"`

	Response json.RawMessage
}

func (entry *LogEntry) UnmarshalJSON(bytes []byte) error {
	var err error
	temp := &jsonLogEntry{}
	if err = json.Unmarshal(bytes, temp); err != nil {
		return err
	}

	*entry = LogEntry{
		Type: temp.Type,
		Time: temp.Time,
		SessionInfo: &SessionInfo{
			Jwt:     temp.SessionInfo.Jwt,
			Nonce:   temp.SessionInfo.Nonce,
			Context: temp.SessionInfo.Context,
			Keys:    make(map[IssuerIdentifier]int),
		},
		Removed:           temp.Removed,
		Disclosed:         temp.Disclosed,
		Received:          temp.Received,
		SignedMessage:     temp.SignedMessage,
		SignedMessageType: temp.SignedMessageType,
		rawResponse:       temp.Response,
	}

	// TODO remove on protocol upgrade
	for iss, count := range temp.SessionInfo.Keys {
		entry.SessionInfo.Keys[NewIssuerIdentifier(iss)] = count
	}

	return nil
}

func (entry *LogEntry) MarshalJSON() ([]byte, error) {
	// If the entry was created using createLogEntry(), then entry.rawResponse == nil
	if len(entry.rawResponse) == 0 && entry.response != nil {
		if bytes, err := json.Marshal(entry.response); err == nil {
			entry.rawResponse = json.RawMessage(bytes)
		} else {
			return nil, err
		}
	}

	var si *logSessionInfo
	if entry.SessionInfo != nil {
		si = &logSessionInfo{
			Jwt:     entry.SessionInfo.Jwt,
			Nonce:   entry.SessionInfo.Nonce,
			Context: entry.SessionInfo.Context,
			Keys:    make(map[string]int),
		}
		// TODO remove on protocol upgrade
		for iss, count := range entry.SessionInfo.Keys {
			si.Keys[iss.String()] = count
		}
	}
	temp := &jsonLogEntry{
		Type:              entry.Type,
		Time:              entry.Time,
		Response:          entry.rawResponse,
		SessionInfo:       si,
		Removed:           entry.Removed,
		Disclosed:         entry.Disclosed,
		Received:          entry.Received,
		SignedMessage:     entry.SignedMessage,
		SignedMessageType: entry.SignedMessageType,
	}

	return json.Marshal(temp)
}