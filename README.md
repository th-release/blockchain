# BlockChain - cth.release

## How To Use?

### API
**HOW TO CONNECT PEER**

**POST /addpeer**  
**Body Example**

```
{
    "address": "localhost:1234"
}
```

**How to Send a Transaction Request After Connecting a Peer**

**POST /transaction**  
**Body Example**

```
{
    "payload": "Example Data"
}
```

After sending a transaction, the network conducts a block mining vote for the transaction once every second, and the transaction is then reflected in the block.

## TODO:
0. CLI 구성 ~~ Ing ...
1. 블록체인 구성 [블록, 트랜잭션, 체인] Ing ...
    * 유저 개념 추가
    * Transaction 서명 개발
2. p2p 구성 [~~Socket~~ -> TCP] Check!
3. 데이터 저장방식 구성 [~~Memory~~ -> ~~Json~~ -> LevelDB] Check!
4. 합의 알고리즘 구성 [PBFT] Check!
5. API 구성 [transaction] ~~ Ing ...
    * cors 설정
6. 코드 리펙토링 ~~ Ing ...