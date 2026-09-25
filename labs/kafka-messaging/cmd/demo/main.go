package main

import (
	"fmt"

	kafka "github.com/Sumit6258/Faultline/labs/kafka-messaging"
)

const (
	numPartitions   = 4
	producedPerTick = 40 // messages produced across the whole topic, per tick
	perConsumerRate = 10 // messages one consumer instance can process per tick
	ticks           = 10
)

func main() {
	fmt.Printf("Topic with %d partitions. %d messages produced per tick. Each consumer processes up to %d per tick.\n", numPartitions, producedPerTick, perConsumerRate)
	fmt.Println()
	for _, numConsumers := range []int{1, 2, 4, 8} {
		runLagScenario(numConsumers)
	}

	demoIdempotency()
	demoDLQ()
}

func runLagScenario(numConsumers int) {
	topic := kafka.NewTopic(numPartitions)
	group := kafka.NewConsumerGroup(topic, numConsumers)

	for tick := 0; tick < ticks; tick++ {
		for i := 0; i < producedPerTick; i++ {
			topic.Produce(fmt.Sprintf("key-%d-%d", tick, i), "payload")
		}

		for c := 0; c < numConsumers && c < numPartitions; c++ {
			budget := perConsumerRate
			for p := 0; p < numPartitions && budget > 0; p++ {
				if group.OwnerOf(p) != c {
					continue
				}
				for _, m := range group.Pending(p) {
					if budget == 0 {
						break
					}
					group.Commit(p, m.Offset)
					budget--
				}
			}
		}
	}

	activeConsumers := numConsumers
	if activeConsumers > numPartitions {
		activeConsumers = numPartitions
	}
	fmt.Printf("%d consumers (%d active, %d idle, only %d partitions exist): lag after %d ticks = %d\n",
		numConsumers, activeConsumers, numConsumers-activeConsumers, numPartitions, ticks, group.TotalLag())
}

func demoIdempotency() {
	fmt.Println()
	fmt.Println("Idempotency: the same message delivered twice, a consumer crash before committing.")

	charged := 0
	consumer := kafka.NewIdempotentConsumer(func(m kafka.Message) error {
		charged++
		return nil
	})

	msg := kafka.Message{Key: "order-789", Value: "charge $42"}
	consumer.Process(msg)
	consumer.Process(msg) // redelivered, same key

	fmt.Printf("message delivered twice, underlying charge ran %d time(s)\n", charged)
}

func demoDLQ() {
	fmt.Println()
	fmt.Println("Dead letter queue: one message that can never succeed, mixed in with ones that do.")

	dlq := &kafka.DLQ{}
	messages := []kafka.Message{
		{Key: "order-1", Value: "ok"},
		{Key: "order-2-poison", Value: "always fails"},
		{Key: "order-3", Value: "ok"},
	}

	const maxAttempts = 3
	for _, m := range messages {
		failed := false
		for attempt := 0; attempt < maxAttempts; attempt++ {
			failed = m.Key == "order-2-poison"
			if !failed {
				break
			}
		}
		if failed {
			dlq.Send(m)
			fmt.Printf("  %s: failed all %d attempts, sent to DLQ\n", m.Key, maxAttempts)
		} else {
			fmt.Printf("  %s: processed successfully\n", m.Key)
		}
	}
	fmt.Printf("DLQ now holds %d message(s), %d processed normally\n", dlq.Len(), len(messages)-dlq.Len())
}
