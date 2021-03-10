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

	InitialOrderQuantity fixedpoint.Value `json:"initialOrderQuantity"`
	CountOfMoreOrders    int              `json:"countOfMoreOrders"`

	// GridNum is the grid number, how many orders you want to post on the orderbook.
	GridNum int `json:"gridNumber"`
	Long bool `json:"long"`

	activeOrders *bbgo.LocalActiveOrderBook

	orders           map[uint64]types.Order
	currentUpperGrid int
	currentLowerGrid int

	currentTotalValue fixedpoint.Value
}

func (s *Strategy) ID() string {
	return "infinitegrid"
}

func (s *Strategy) placeInfiniteGridOrders(orderExecutor bbgo.OrderExecutor, session *bbgo.ExchangeSession) {
	quoteCurrency := s.Market.QuoteCurrency
	balances := session.Account.Balances()

	if s.currentUpperGrid != 0 || s.currentLowerGrid != 0 {
		// reconnect, do not place orders
		return
	}

	balance, ok := balances[quoteCurrency]
	if !ok || balance.Available <= 0 {
		return
	}

	var orders []types.SubmitOrder
	var quantityF float64
	currentPrice, ok := session.LastPrice(s.Symbol)
	if !ok {
		return
	}

	s.currentTotalValue = s.Budget
	//currentPriceF := fixedpoint.NewFromFloat(currentPrice)
	if s.InitialOrderQuantity > 0 {
		quantityF = s.InitialOrderQuantity.Float64()
	} else {
		quantityF = s.Quantity.Float64() / (1 - 1/(1.0+s.Margin.Float64()))
	}

	// Buy half of value of asset
	order := types.SubmitOrder{
		Symbol:      s.Symbol,
		Side:        types.SideTypeBuy,
		Type:        types.OrderTypeLimit,
		Market:      s.Market,
		Quantity:    quantityF,
		Price:       currentPrice,
		TimeInForce: "GTC",
	}
	log.Infof("submitting order: %s", order.String())
	orders = append(orders, order)

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
		s.currentUpperGrid++
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
		s.currentLowerGrid++
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
	var quantity = order.Quantity
	const earlyPlacedCount = 2

	if order.Quantity == s.InitialOrderQuantity.Float64() {
		return
	}

	switch side {
	case types.SideTypeSell:
		price = order.Price * (1.0 + s.Margin.Float64())
		s.currentUpperGrid++
		s.currentLowerGrid--
		if s.Long {
			quantity = s.Quantity.Float64()
		}

	case types.SideTypeBuy:
		price = order.Price * (1.0 - s.Margin.Float64())
		if price < s.LowerPrice.Float64() {
			return
		}
		if s.Long {
			var amount = order.Price * order.Quantity
			quantity = amount / price
		}
		s.currentUpperGrid--
		s.currentLowerGrid++
	}

	submitOrder := types.SubmitOrder{
		Symbol:      s.Symbol,
		Side:        side,
		Type:        types.OrderTypeLimit,
		Market:      s.Market,
		Quantity:    quantity,
		Price:       price,
		TimeInForce: "GTC",
	}

	if price >= s.LowerPrice.Float64() {
		log.Infof("submitting order: %s, currentUpperGrid: %d, currentLowerGrid: %d", submitOrder.String(), s.currentUpperGrid, s.currentLowerGrid)
		orders = append(orders, submitOrder)
	}

	if order.Side == types.SideTypeSell && s.currentUpperGrid <= earlyPlacedCount {
		// Plase a more higher order
		for i := 1; i <= s.CountOfMoreOrders; i++ {
			price = order.Price * math.Pow((1.0+s.Margin.Float64()), float64(i+earlyPlacedCount))
			submitOrder := types.SubmitOrder{
				Symbol:      s.Symbol,
				Side:        order.Side,
				Market:      s.Market,
				Type:        types.OrderTypeLimit,
				Quantity:    s.Quantity.Float64(),
				Price:       price,
				TimeInForce: "GTC",
			}

			orders = append(orders, submitOrder)
			s.currentUpperGrid++
			log.Infof("submitting order: %s, currentUpperGrid: %d", submitOrder.String(), s.currentUpperGrid)
		}
	}

	if order.Side == types.SideTypeBuy && s.currentLowerGrid <= earlyPlacedCount {
		// Plase a more lower order
		for i := 1; i <= s.CountOfMoreOrders; i++ {
			price = order.Price * math.Pow((1.0-s.Margin.Float64()), float64(i+earlyPlacedCount))

			if price < s.LowerPrice.Float64() {
				break
			}

			submitOrder := types.SubmitOrder{
				Symbol:      s.Symbol,
				Side:        order.Side,
				Market:      s.Market,
				Type:        types.OrderTypeLimit,
				Quantity:    s.Quantity.Float64(),
				Price:       price,
				TimeInForce: "GTC",
			}

			orders = append(orders, submitOrder)
			s.currentLowerGrid++
			log.Infof("submitting order: %s, currentLowerGrid: %d", submitOrder.String(), s.currentLowerGrid)

		}
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
        s.Notifiability.Notify("order update: %s", order.String())

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
	s.currentLowerGrid = 0
	s.currentUpperGrid = 0

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
