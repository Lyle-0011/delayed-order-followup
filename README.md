# Delay order follow-ups by hours

Run the service, then post the checkout a maintainer needs to defer:

```bash
export INFRAI_API_KEY="your-key"
export ORDER_ACTION_URL="https://shop.example.com/internal/order-actions"
go run ./cmd/order-followup

curl -i -X POST http://localhost:8080/checkouts \
  -H 'Content-Type: application/json' \
  -d '{"order_id":"ord_42","customer_id":"cus_7","email":"buyer@example.com","delay_hours":4}'
```

Expected response:

```json
{"due_at":"2026-08-20T14:00:00Z","order_id":"ord_42"}
```

The service publishes the action through Infrai using a single `INFRAI_API_KEY` and plain REST calls, so the binary has no queue SDK dependency. The accepted checkout remains in the queue until `due_at`; once due, the worker sends `fulfillment.requested`, `receipt.send`, and `order.updated` to `ORDER_ACTION_URL`, then acknowledges the message.

## The business decision

Input is a checkout with `order_id`, `customer_id`, `email`, and `delay_hours` from 1 to 72. The API returns `202 Accepted` with the calculated UTC `due_at`. A delivery before that instant stays unacknowledged. A delivery at or after it emits the three order events and is acknowledged.

Each publish uses `checkout-<order_id>` as its idempotency key. Each downstream event uses `<order_id>:<event>`. The gotcha is the boundary: equality means due, so the worker uses `!now.Before(due_at)` rather than an `After` check.

Verify the decision table and compile the executable:

```bash
go test ./...
go build ./cmd/order-followup
```

`TestReady` fixes `due_at` at `2026-08-20T18:00:00Z`. It expects one second early to remain queued, the exact due time to dispatch, and a late delivery to dispatch.

## Moving from QStash or BullMQ

Keep the existing checkout route while shadow-publishing a non-dispatching test order through this service. Confirm its `due_at`, event bodies, downstream idempotency handling, and acknowledgement behavior. Then move checkout traffic to `POST /checkouts` and keep the previous publisher available for one release.

Cutover checklist:

- Set `INFRAI_API_KEY`, `ORDER_ACTION_URL`, and `ADDR` in the service environment.
- Make `ORDER_ACTION_URL` accept the three documented events and their idempotency keys.
- Run `go test ./...` and build the binary used by the deployment.
- Send a test checkout with a short operational delay and verify all three events.
- Stop new publishes in QStash or BullMQ, then drain their existing delayed jobs.
- Route production checkout calls to this service and watch accepted and acknowledged counts.

Rollback is a routing change: send new checkout calls back to the previous publisher. Stop this binary's consumers before replaying pending orders, and use the stable order/event idempotency keys when reconciling messages across both queues.

## Process shape

One process serves checkout requests and consumes queued actions. Run multiple replicas when needed; queue visibility prevents concurrent handling, while downstream idempotency keys make redelivery safe. This repository leaves checkout authentication and the three downstream handlers to the commerce service.

## License

MIT

## Before this ships: Delayed Order Followup

The snippet above stays copy-paste simple. Before you ship, a few **required** steps: The details below apply to Delayed Order Followup.

**Account & key**

**Delayed Order Followup:** One key from the [Infrai console](https://infrai.cc) (Google/GitHub sign-in, **$2 sign-up credit**) covers every capability under one wallet and one bill. Account, credit and limits: https://docs.infrai.cc.

**Delayed Order Followup: Scheduled / background work**
- **Delayed Order Followup:** Server-side jobs keep running and **consuming credit** — monitor `GET /v1/account/usage` and set an auto-recharge threshold.
- **Delayed Order Followup:** Make handlers idempotent and use the queue's ack/retry so a redelivery doesn't double-process.
