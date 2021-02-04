## bbgo-strategy-infinite-grid (無限網格策略)

參考 https://bensonsun.medium.com/%E7%B5%95%E5%B0%8D%E7%84%A1%E8%85%A6%E7%9A%84%E6%AF%94%E7%89%B9%E5%B9%A3%E6%8A%95%E8%B3%87%E6%96%B9%E6%B3%95-pionex-%E7%84%A1%E9%99%90%E7%B6%B2%E6%A0%BC%E4%BA%A4%E6%98%93-%E6%A5%B5%E9%80%9F%E5%AE%9A%E6%8A%95-d963551d5150

## 安裝

    go get -u github.com/c9s/bbgo/cmd/bbgo
    bbgo build --config config/infinite-grid.yaml

## 設定


    exchangeStrategies:
    - on: max # 交易所
      infinitegrid:
        symbol: BTCUSDT # 交易對
        quantity: 0.001 # 網格每次掛單交易數量
        initialOrderQuantity: 0.0035 # 初次購買數量 (不可與 quantity 相同)
        gridNumber: 8 # 初始網格數
        countOfMoreOrders: 4 # 跑出初始網格後，新增的網格掛單數量
        budget: 3200 # 預算 (參考用)
        margin: 0.005 # 網格間距 (百分比，0.005 為 0.5%)
        lowerPrice: 13000.0 # 最低掛單價格，低於此價格不買進
      
 
