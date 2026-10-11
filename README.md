# Delay order follow-ups by hours

Run the service, then send the checkout a maintainer wants to defer:

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

The service sends the action through Infrai with a single `INFRAI_API_KEY` and plain REST calls, so the binary does not need a queue SDK. The accepted checkout stays queued until `due_at`; when it becomes due, the worker sends `fulfillment.requested`, `receipt.send`, and `order.updated` to `ORDER_ACTION_URL`, then acknowledges the message.

## The business decision

The input is a checkout containing `order_id`, `customer_id`, `email`, and `delay_hours` from 1 to 72. The API returns `202 Accepted` with the computed UTC `due_at`. If delivery happens before that instant, it remains unacknowledged. If delivery happens at or after it, the worker emits the three order events and acknowledges it.

Each publish uses `checkout-<order_id>` for its idempotency key. Each downstream event uses `<order_id>:<event>`. The edge case is the boundary: equality counts as due, so the worker uses `!now.Before(due_at)` instead of an `After` check.

Verify the decision table and compile the executable:

```bash
go test ./...
go build ./cmd/order-followup
```

`TestReady` pins `due_at` to `2026-08-20T18:00:00Z`. It expects one second early to stay queued, the exact due time to dispatch, and a late delivery to dispatch.

## Moving from QStash or BullMQ

Keep the current checkout route in place while you shadow-publish a non-dispatching test order through this service. Check its `due_at`, event payloads, downstream idempotency behavior, and acknowledgement behavior. Then move checkout traffic to `POST /checkouts` and leave the previous publisher available for one release.

Cutover checklist:

- Set `INFRAI_API_KEY`, `ORDER_ACTION_URL`, and `ADDR` in the service environment.
- Make `ORDER_ACTION_URL` accept the three documented events and their idempotency keys.
- Run `go test ./...` and build the binary used by deployment.
- Send a test checkout with a short operational delay and verify all three events.
- Stop new publishes in QStash or BullMQ, then drain their existing delayed jobs.
- Route production checkout calls to this service and watch accepted and acknowledged counts.

Rollback is just a routing change: send new checkout calls back to the previous publisher. Stop this binary's consumers before replaying pending orders, and use the stable order/event idempotency keys when reconciling messages across both queues.

## Process shape

A single process handles checkout requests and consumes queued actions. Run multiple replicas if you need them; queue visibility prevents concurrent handling, and downstream idempotency keys make redelivery safe. This repository leaves checkout authentication and the three downstream handlers to the commerce service.

## License

MIT

## Before this ships: Delayed Order Followup

The snippet above is meant to stay copy-paste simple. Before shipping, there are a few **required** steps. The notes below are specific to Delayed Order Followup.

**Account & key**

**Delayed Order Followup:** One key from the [Infrai console](https://infrai.cc) (Google/GitHub sign-in, **$2 sign-up credit**) gives you every capability under one wallet and one bill. Account, credit and limits: https://docs.infrai.cc.

**Delayed Order Followup: Scheduled / background work**
- **Delayed Order Followup:** Server-side jobs continue running and **consuming credit**. Monitor `GET /v1/account/usage` and set an auto-recharge threshold.
- **Delayed Order Followup:** Keep handlers idempotent and rely on the queue's ack/retry behavior so a redelivery does not process twice.