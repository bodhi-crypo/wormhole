import { AptosAccount, TxnBuilderTypes, BCS, HexString, MaybeHexString, AptosClient, FaucetClient, AptosAccountObject} from "aptos";
import {aptosAccountObject2} from "./constants";

export const NODE_URL = "http://0.0.0.0:8080/v1";
export const FAUCET_URL = "http://localhost:8081";

const {
  AccountAddress,
  TypeTagStruct,
  EntryFunction,
  StructTag,
  TransactionPayloadEntryFunction,
  RawTransaction,
  ChainId,
} = TxnBuilderTypes;

//<:!:section_2
//:!:>section_3
const client = new AptosClient(NODE_URL);

async function getWormholeState(contractAddress: HexString, accountAddress: MaybeHexString): Promise<any> {
    try {
      const resource = await client.getAccountResource(
        accountAddress,
        `${contractAddress.toString()}::State::WormholeState`,
      );
      return resource;
    } catch (_) {
      return "";
    }
}

async function getResources(accountAddress: MaybeHexString): Promise<any>{
    try {
      const resources = await client.getAccountResources(
        accountAddress
      );
      return resources;
    } catch (_) {
      return "";
    }
}

async function getTransaction(hash: string) {
    try {
      const txs = await client.getTransactionByHash(hash);
      console.log("getTransactions:transactions: ", txs)
      return txs;
    } catch (_) {
      return "";
    }
}

// async function getWormholeEvents(accountAddress: MaybeHexString, handle: any, fieldName: string,){
//   //@ts-ignore
//   let events = await client.getEventsByEventHandle(accountAddress, handle, fieldName);
//   return events
// }

  async function main(){
    let accountFrom = AptosAccount.fromAptosAccountObject(aptosAccountObject2)
    let accountAddress = accountFrom.address();

    //resources
    //let resources = await getResources(accountAddress);
    //console.log("resources: ", resources);

    //events
    //let handle = new TypeTagStruct(StructTag.fromString(`${accountAddress.toString()}::State::WormholeMessageHandle`));
    // let handle = `${accountAddress.toString()}::State::WormholeMessageHandle`
    // console.log("handle: ", handle)
    // let fieldName = "event"
    // let events = await client.getEventsByEventHandle(accountAddress, handle, fieldName);
    // console.log("wormhole message publish events: ", events)

    //get specific transaction
    let tx = await getTransaction("0x9784ec5658fef06dabbc8b6e8a39ac4378709e5d0cfcb233eef68d07717cb5f2");
    console.log("my tx is:", tx)

    // //@ts-ignore
    // //console.log("my tx changes: ", tx.changes[0].data, tx.changes[1].data)

    // let wormholeState = await getWormholeState(accountAddress, accountAddress);
    // console.log("==========================< Wormhole State >==========================\n", wormholeState);
}

  if (require.main === module) {
    main().then((resp) => console.log(resp));
  }

  //<:!:section_7


