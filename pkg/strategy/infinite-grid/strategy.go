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
	Quantity fixedpoint.Value `json:"quantity"`

	// GridNum is the grid number, how many orders you want to post on the orderbook.
	GridNum int `json:"gridNumber"`

	activeOrders *bbgo.LocalActiveOrderBook

	orders           map[uint64]types.Order
	currentUpperGrid int

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
	s.currentUpperGrid = s.GridNum / 2

	// Sell Side
	for i := 1; i <= s.GridNum/2; i++ {
		price := currentPrice * math.Pow((1.0+s.Margin.Float64()), float64(i))

		order := types.SubmitOrder{
			Symbol:      s.Symbol,
			Side:        types.SideTypeSell,
			Type:        types.OrderTypeLimit,
			Market:      s.Market,
			Quantity:    s.Quantity.Float64(),
			Price:       price,
			TimeInForce: "GTC",
		}
		log.Infof("submitting order: %s", order.String())
		orders = append(orders, order)
	}

	// Buy Side
	for i := 1; i <= s.GridNum/2; i++ {
		price := currentPrice * math.Pow((1.0-s.Margin.Float64()), float64(i))

		if price < s.LowerPrice.Float64() {
			break
		}
		order := types.SubmitOrder{
			Symbol:      s.Symbol,
			Side:        types.SideTypeBuy,
			Type:        types.OrderTypeLimit,
			Market:      s.Market,
			Quantity:    s.Quantity.Float64(),
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

func (s *Strategy) submitFollowingOrder(order types.Order) {
	var side = order.Side.Reverse()
	var orders []types.SubmitOrder
	var price float64

	if order.Quantity != s.Quantity.Float64() {
		return
	}

	switch side {
	case types.SideTypeSell:
		price = order.Price * (1.0 + s.Margin.Float64())
		s.currentUpperGrid++

	case types.SideTypeBuy:
		price = order.Price * (1.0 - s.Margin.Float64())
		s.currentUpperGrid--
	}

	submitOrder := types.SubmitOrder{
		Symbol:      s.Symbol,
		Side:        side,
		Type:        types.OrderTypeLimit,
		Market:      s.Market,
		Quantity:    order.Quantity,
		Price:       price,
		TimeInForce: "GTC",
	}

	if price >= s.LowerPrice.Float64() {
		log.Infof("submitting order: %s, currentUpperGrid: %d", submitOrder.String(), s.currentUpperGrid)
		orders = append(orders, submitOrder)
	}

	if order.Side == types.SideTypeSell && s.currentUpperGrid <= 0 {
		// Plase a more higher order
		price = order.Price * (1.0 + s.Margin.Float64())
		s.currentUpperGrid++
		submitOrder := types.SubmitOrder{
			Symbol:      s.Symbol,
			Side:        order.Side,
			Market:      s.Market,
			Type:        types.OrderTypeLimit,
			Quantity:    order.Quantity,
			Price:       price,
			TimeInForce: "GTC",
		}

		log.Infof("submitting order: %s, currentUpperGrid: %d", submitOrder.String(), s.currentUpperGrid)
		orders = append(orders, submitOrder)
	}

	createdOrders, err := s.OrderExecutor.SubmitOrders(context.Background(), orders...)
	if err != nil {
		log.WithError(err).Errorf("can not place orders")
		return
	}

	s.activeOrders.Add(createdOrders...)
}

func (s *Strategy) orderUpdateHandler(order types.Order) {
	if order.Symbol != s.Symbol {
		return
	}

	log.Infof("order update: %s", order.String())

	switch order.Status {
	case types.OrderStatusFilled:
		s.activeOrders.Remove(order)
		s.submitFollowingOrder(order)

	case types.OrderStatusPartiallyFilled, types.OrderStatusNew:
		s.activeOrders.Update(order)

	case types.OrderStatusCanceled, types.OrderStatusRejected:
		log.Infof("order status %s, removing %d from the active order pool...", order.Status, order.OrderID)
		s.activeOrders.Remove(order)
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
	session.Stream.OnConnect(func() {
		s.placeInfiniteGridOrders(orderExecutor, session)
	})

	return nil
}
