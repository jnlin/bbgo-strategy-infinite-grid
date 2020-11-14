package skeleton

import (
	"context"
	"math"
	"sync"

	log "github.com/sirupsen/logrus"

	"github.com/c9s/bbgo/pkg/bbgo"
	"github.com/c9s/bbgo/pkg/fixedpoint"
	"github.com/c9s/bbgo/pkg/types"
)

func init() {
	bbgo.RegisterStrategy("infinitegrid", &Strategy{})
}

type Strategy struct {
	*bbgo.Notifiability

	*bbgo.Graceful

	bbgo.OrderExecutor

	Symbol string `json:"symbol"`

	types.Market

	// Max budget of the strategy
	Budget fixedpoint.Value `json:"budget"`

	LowerPrice fixedpoint.Value `json:"lowerPrice"`

	// Buy-Sell Margin for each pair of orders
	Margin fixedpoint.Value `json:"margin"`

	// Quantity is the quantity you want to submit for each order.
	Quantity float64 `json:"quantity"`

	// GridNum is the grid number, how many orders you want to post on the orderbook.
	GridNum int `json:"gridNumber"`

	activeOrders *bbgo.LocalActiveOrderBook

	orders map[uint64]types.Order

	currentTotalValue fixedpoint.Value
}

func (s *Strategy) placeInfiniteGridOrders(orderExecutor bbgo.OrderExecutor, session *bbgo.ExchangeSession) {
	quoteCurrency := s.Market.QuoteCurrency
	balances := session.Account.Balances()

	balance, ok := balances[quoteCurrency]
	if !ok || balance.Available <= 0 {
		return
	}

	var orders []types.SubmitOrder
	currentPrice, ok := session.LastPrice(s.Symbol)
	if !ok {
		return
	}

	s.currentTotalValue = s.Budget
	currentPriceF := fixedpoint.NewFromFloat(currentPrice)
	quantity := s.Budget / 2 / currentPriceF

	// Buy half of value of asset
	order := types.SubmitOrder{
		Symbol:      s.Symbol,
		Side:        types.SideTypeBuy,
		Type:        types.OrderTypeLimit,
		Market:      s.Market,
		Quantity:    quantity.Float64(),
		Price:       currentPrice,
		TimeInForce: "GTC",
	}
	log.Infof("submitting order: %s", order.String())
	orders = append(orders, order)

	// Sell Side
	for i := 1; i <= s.GridNum/2; i++ {
		price := currentPrice * math.Pow((1+s.Margin).Float64(), float64(i))

		order := types.SubmitOrder{
			Symbol:      s.Symbol,
			Side:        types.SideTypeSell,
			Type:        types.OrderTypeLimit,
			Market:      s.Market,
			Quantity:    s.Quantity,
			Price:       price,
			TimeInForce: "GTC",
		}
		log.Infof("submitting order: %s", order.String())
		orders = append(orders, order)
	}

	// Buy Side
	for i := 1; i <= s.GridNum/2; i++ {
		price := currentPrice * math.Pow((1-s.Margin).Float64(), float64(i))

		order := types.SubmitOrder{
			Symbol:      s.Symbol,
			Side:        types.SideTypeBuy,
			Type:        types.OrderTypeLimit,
			Market:      s.Market,
			Quantity:    s.Quantity,
			Price:       price,
			TimeInForce: "GTC",
		}
		log.Infof("submitting order: %s", order.String())
		orders = append(orders, order)
	}

	createdOrders, err := orderExecutor.SubmitOrders(context.Background(), orders...)
	if err != nil {
		log.WithError(err).Errorf("can not place orders")
		return
	}

	s.activeOrders.Add(createdOrders...)
}

func (s *Strategy) tradeUpdateHandler(trade types.Trade) {
	if trade.Symbol != s.Symbol {
		return
	}
}

func (s *Strategy) orderUpdateHandler(order types.Order) {
	if order.Symbol != s.Symbol {
		return
	}
}

func (s *Strategy) Subscribe(session *bbgo.ExchangeSession) {
	session.Subscribe(types.KLineChannel, s.Symbol, types.SubscribeOptions{Interval: "1m"})
}

func (s *Strategy) Run(ctx context.Context, orderExecutor bbgo.OrderExecutor, session *bbgo.ExchangeSession) error {
	if s.GridNum == 0 {
		s.GridNum = 10
	}

	s.orders = make(map[uint64]types.Order)
	s.activeOrders = bbgo.NewLocalActiveOrderBook()

	s.Graceful.OnShutdown(func(ctx context.Context, wg *sync.WaitGroup) {
		defer wg.Done()

		log.Infof("canceling active orders...")

		if err := session.Exchange.CancelOrders(ctx, s.activeOrders.Orders()...); err != nil {
			log.WithError(err).Errorf("cancel order error")
		}
	})

	session.Stream.OnOrderUpdate(s.orderUpdateHandler)
	session.Stream.OnTradeUpdate(s.tradeUpdateHandler)
	session.Stream.OnConnect(func() {
		s.placeInfiniteGridOrders(orderExecutor, session)
	})

	return nil
}
