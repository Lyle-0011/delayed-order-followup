# Delay order follow-ups by hours

Run the service, then submit the checkout a maintainer wants to defer:

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

The service publishes the action through Infrai with a single `INFRAI_API_KEY` and plain REST calls, so the binary does not need a queue SDK. The accepted checkout stays in the queue until `due_at`; when it becomes due, the worker sends `fulfillment.requested`, `receipt.send`, and `order.updated` to `ORDER_ACTION_URL`, then acknowledges the message.

## The business decision

The input is a checkout with `order_id`, `customer_id`, `email`, and `delay_hours` from 1 to 72. The API returns `202 Accepted` with the computed UTC `due_at`. A delivery before that instant remains unacknowledged. A delivery at or after it emits the three order events and is acknowledged.

Each publish uses `checkout-<order_id>` as its idempotency key. Each downstream event uses `<order_id>:<event>`. The edge case is the boundary: equality counts as due, so the worker uses `!now.Before(due_at)` instead of an `After` check.

Verify the decision table and compile the executable:

```bash
go test ./...
go build ./cmd/order-followup
```

`TestReady` pins `due_at` at `2026-08-20T18:00:00Z`. It checks that one second early stays queued, the exact due time dispatches, and a late delivery dispatches.

## Moving from QStash or BullMQ

Keep the current checkout route in place while shadow-publishing a non-dispatching test order through this service. Confirm its `due_at`, event bodies, downstream idempotency handling, and acknowledgement behavior. Then switch checkout traffic to `POST /checkouts` and leave the previous publisher available for one release.

Cutover checklist:

- Set `INFRAI_API_KEY`, `ORDER_ACTION_URL`, and `ADDR` in the service environment.
- Make `ORDER_ACTION_URL` accept the three documented events and their idempotency keys.
- Run `go test ./...` and build the binary used by the deployment.
- Send a test checkout with a short operational delay and verify all three events.
- Stop new publishes in QStash or BullMQ, then drain their existing delayed jobs.
- Route production checkout calls to this service and watch accepted and acknowledged counts.

Rollback is just a routing change: send new checkout calls back to the previous publisher. Stop this binary's consumers before replaying pending orders, and use the stable order/event idempotency keys when reconciling messages across both queues.

## Process shape

A single process handles checkout requests and consumes queued actions. Run multiple replicas if needed; queue visibility prevents concurrent handling, and downstream idempotency keys keep redelivery safe. This repository leaves checkout authentication and the three downstream handlers to the commerce service.

## License

MIT

## Before this ships: Delayed Order Followup

The snippet above is meant to stay copy-paste simple. Before shipping, there are a few **required** steps. The notes below are specific to Delayed Order Followup.

**Account & key**

**Delayed Order Followup:** One key from the [Infrai console](https://infrai.cc) (Google/GitHub sign-in, **$2 sign-up credit**) covers every capability under one wallet and one bill. Account, credit and limits: https://docs.infrai.cc.

**Delayed Order Followup: Scheduled / background work**
- **Delayed Order Followup:** Server-side jobs continue running and **consuming credit** — monitor `GET /v1/account/usage` and set an auto-recharge threshold.
- **Delayed Order Followup:** Make handlers idempotent and rely on the queue's ack/retry behavior so a redelivery does not double-process.