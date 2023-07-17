package breakwater

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCreditsToOvercommit(t *testing.T) {
	bw := InitBreakwater(BWParametersDefault)
	<-bw.numClients
	bw.numClients <- 21
	<-bw.cIssued
	bw.cIssued <- 200
	bw.cTotal = 5000
	expected := roundedInt(float64(5000-200) / float64(21))
	credits := bw.calculateCreditsToOvercommit()
	if credits != expected {
		t.Errorf("Expected credits to be %d, got %d", expected, credits)
	}
}

func TestGetHigherCreditsIssuedCapped(t *testing.T) {
	bw := InitBreakwater(BWParametersDefault)
	<-bw.cIssued
	bw.cIssued <- 300
	bw.cTotal = 310
	var (
		cOvercommit int64 = 10
		cPrevious   int64 = 30
		demand      int64 = 31
		expected    int64 = 40
	)
	actual := bw.getHigherCreditsIssued(cOvercommit, demand, cPrevious)

	if actual != expected {
		t.Errorf("Expected credits to be %d, got %d", expected, actual)
	}
}

func TestGetHigherCreditsIssuedUnCapped(t *testing.T) {
	bw := InitBreakwater(BWParametersDefault)
	<-bw.cIssued
	bw.cIssued <- 300
	bw.cTotal = 4000
	var (
		cOvercommit int64 = 10
		cPrevious   int64 = 30
		demand      int64 = 31
		expected    int64 = 10 + 31
	)
	actual := bw.getHigherCreditsIssued(cOvercommit, demand, cPrevious)

	if actual != expected {
		t.Errorf("Expected credits to be %d, got %d", expected, actual)
	}
}

func TestGetLowerCreditsIssuedDemandLowered(t *testing.T) {
	bw := InitBreakwater(BWParametersDefault)
	var (
		cOvercommit int64 = 10
		cPrevious   int64 = 30
		demand      int64 = 10
		expected    int64 = 10 + 10
	)
	actual := bw.getLowerCreditsIssued(cOvercommit, demand, cPrevious)

	if actual != expected {
		t.Errorf("Expected credits to be %d, got %d", expected, actual)
	}
}

func TestGetLowerCreditsIssuedDemandRaised(t *testing.T) {
	bw := InitBreakwater(BWParametersDefault)
	var (
		cOvercommit int64 = 10
		cPrevious   int64 = 30
		demand      int64 = 31
		expected    int64 = 30 - 1
	)
	actual := bw.getLowerCreditsIssued(cOvercommit, demand, cPrevious)

	if actual != expected {
		t.Errorf("Expected credits to be %d, got %d", expected, actual)
	}
}

/*
Test for calculating new credits to issue less issued case

1. Create 2 connections

2. Update RTT

3. Update credits issued to client so that it goes down
*/
func TestCreditsIssuedSimpleDecrease(t *testing.T) {
	bw := InitBreakwater(BWParametersDefault)

	<-bw.numClients
	bw.numClients <- 2
	<-bw.cIssued
	bw.cIssued <- 60
	bw.cTotal = 40

	clientId1 := uuid.New()
	clientId2 := uuid.New()
	bw.RegisterClient(clientId1, 30)
	bw.RegisterClient(clientId2, 30)

	// set to 60
	c1, _ := bw.clientMap.Load(clientId1)
	conn1 := c1.(Connection)
	conn1.issued = 60
	bw.clientMap.Store(clientId1, conn1)

	// The delay is < threshold, so additive
	// adds max(2 * 0.001,1) = 1, so cTotal=51
	bw.rttUpdate()

	// cIssued > cTotal, so should decrement,
	// Takes min(30+1, 60-1) = 31
	bw.updateCreditsToIssue(clientId1, 30)

	var expectedCreditsClient1 int64 = 31
	var expectedcIssued int64 = 31

	actualCIssued := <-bw.cIssued
	bw.cIssued <- actualCIssued

	c1, ok := bw.clientMap.Load(clientId1)
	if !ok {
		t.Errorf("Expected client1 to be in clientMap")
	}
	actualCreditsClient1 := c1.(Connection).issued

	actualId := c1.(Connection).id
	if actualId != clientId1 {
		t.Errorf("Expected client1 id to be %s, got %s", clientId1, actualId)
	}

	if actualCreditsClient1 != expectedCreditsClient1 {
		t.Errorf("Expected client1 credits to be %d, got %d", expectedCreditsClient1, actualCreditsClient1)
	}

	if actualCIssued != expectedcIssued {
		t.Errorf("Expected cIssued credits to be %d, got %d", expectedcIssued, actualCIssued)
	}
}

/*
1. Create 2 connections

2. Update RTT

3. Update credits issued to client so that it goes up
*/
func TestCreditsIssuedSimpleIncrease(t *testing.T) {
	bw := InitBreakwater(BWParametersDefault)

	<-bw.cIssued
	bw.cIssued <- 40
	bw.cTotal = 60

	clientId1 := uuid.New()
	clientId2 := uuid.New()
	bw.RegisterClient(clientId1, 20)
	bw.RegisterClient(clientId2, 20)

	// set to issued 20
	c1, _ := bw.clientMap.Load(clientId1)
	conn1 := c1.(Connection)
	conn1.issued = 20
	bw.clientMap.Store(clientId1, conn1)

	// set to issued 20
	c2, _ := bw.clientMap.Load(clientId2)
	conn2 := c2.(Connection)
	conn2.issued = 20
	bw.clientMap.Store(clientId2, conn2)

	// Sleep so that we can update cTotal
	time.Sleep(200 * time.Millisecond)

	// The delay is < threshold, so additive
	// adds max(2 * 0.001,1) = 1, so cTotal=61
	bw.rttUpdate()

	// cIssued < cTotal, so should increment,
	// cOvercommit is 61-40 / 2 = 10.5, so should round up to 11
	// Takes min(30+11, 20+21) = 41
	bw.updateCreditsToIssue(clientId1, 30)

	var expectedCreditsClient1 int64 = 20 + 21
	// cIssued also increments by same amount
	var expectedcIssued int64 = 40 + 21

	actualCIssued := <-bw.cIssued
	bw.cIssued <- actualCIssued

	c1, ok := bw.clientMap.Load(clientId1)
	if !ok {
		t.Errorf("Expected client1 to be in clientMap")
	}
	actualCreditsClient1 := c1.(Connection).issued

	actualId := c1.(Connection).id
	if actualId != clientId1 {
		t.Errorf("Expected client1 id to be %s, got %s", clientId1, actualId)
	}

	if actualCreditsClient1 != expectedCreditsClient1 {
		t.Errorf("Expected client1 credits to be %d, got %d", expectedCreditsClient1, actualCreditsClient1)
	}

	if actualCIssued != expectedcIssued {
		t.Errorf("Expected cIssued credits to be %d, got %d", expectedcIssued, actualCIssued)
	}
}

/*
1. Create 2 connections
2. Update RTT
3. Update credits issued to client so that it changes accordingly
4. Update credits issued to client multiple times so that it goes down by 1
*/
func TestCreditsIssuedIssuesOnlyOnceAfterUpdate(t *testing.T) {
	bw := InitBreakwater(BWParametersDefault)

	<-bw.cIssued
	bw.cIssued <- 40
	bw.cTotal = 60

	clientId1 := uuid.New()
	clientId2 := uuid.New()
	bw.RegisterClient(clientId1, 20)
	bw.RegisterClient(clientId2, 20)

	// set to issued 20
	c1, _ := bw.clientMap.Load(clientId1)
	conn1 := c1.(Connection)
	conn1.issued = 20
	bw.clientMap.Store(clientId1, conn1)

	// set to issued 20
	c2, _ := bw.clientMap.Load(clientId2)
	conn2 := c2.(Connection)
	conn2.issued = 20
	bw.clientMap.Store(clientId2, conn2)

	// Sleep so that we can update cTotal
	time.Sleep(200 * time.Millisecond)

	// The delay is < threshold, so additive
	// adds max(2 * 0.001,1) = 1, so cTotal=61
	bw.rttUpdate()

	// cIssued < cTotal, so should increment,
	// cOvercommit is 61-40 / 2 = 10.5, so should round up to 11
	// Takes min(30+11, 20+21) = 41
	go bw.updateCreditsToIssue(clientId1, 30)
	// decrement by 1 = 40
	go bw.updateCreditsToIssue(clientId1, 30)
	// decrement by 1 = 39
	go bw.updateCreditsToIssue(clientId1, 30)

	// Allow go-routines to execute
	time.Sleep(200 * time.Millisecond)

	var expectedCreditsClient1 int64 = 20 + 21 - 2
	// cIssued also increments by same amount
	var expectedcIssued int64 = 40 + 21 - 2

	actualCIssued := <-bw.cIssued
	bw.cIssued <- actualCIssued

	c1, ok := bw.clientMap.Load(clientId1)
	if !ok {
		t.Errorf("Expected client1 to be in clientMap")
	}
	actualCreditsClient1 := c1.(Connection).issued

	actualId := c1.(Connection).id
	if actualId != clientId1 {
		t.Errorf("Expected client1 id to be %s, got %s", clientId1, actualId)
	}

	if actualCreditsClient1 != expectedCreditsClient1 {
		t.Errorf("Expected client1 credits to be %d, got %d", expectedCreditsClient1, actualCreditsClient1)
	}

	if actualCIssued != expectedcIssued {
		t.Errorf("Expected cIssued credits to be %d, got %d", expectedcIssued, actualCIssued)
	}
}

/*
1. Create 2 connections
2. Update RTT
3. Update credits issued to client so that it changes accordingly
4. Update credits issued to client so that it goes down by 1
5. Update RTT
6. Update credits issued to client so that it changes accordingly
*/
func TestCreditsIssuedInterlacedRttUpdate(t *testing.T) {
	bw := InitBreakwater(BWParametersDefault)

	<-bw.cIssued
	bw.cIssued <- 40
	bw.cTotal = 30

	clientId1 := uuid.New()
	clientId2 := uuid.New()
	// demand of 10
	bw.RegisterClient(clientId1, 10)
	bw.RegisterClient(clientId2, 10)

	// set to issued 20
	c1, _ := bw.clientMap.Load(clientId1)
	conn1 := c1.(Connection)
	conn1.issued = 20
	bw.clientMap.Store(clientId1, conn1)

	// set to issued 20
	c2, _ := bw.clientMap.Load(clientId2)
	conn2 := c2.(Connection)
	conn2.issued = 20
	bw.clientMap.Store(clientId2, conn2)

	// Sleep so that we can update cTotal
	time.Sleep(200 * time.Millisecond)

	// The delay is < threshold, so additive
	// adds max(2 * 0.001,1) = 1, so cTotal=31
	bw.rttUpdate()

	// cIssued > cTotal, so should decrement,
	// cOvercommit is 1
	// Takes min(10+1, 20-1) = 11
	go bw.updateCreditsToIssue(clientId1, 10)
	// decrement by 1 = 10
	go bw.updateCreditsToIssue(clientId1, 10)
	// decrement by 1 = 9
	go bw.updateCreditsToIssue(clientId1, 10)

	// Allow go-routines to execute and for rttUpdate
	time.Sleep(200 * time.Millisecond)

	// At this point, cIssued should be 40 - 11 = 29
	expectedcIssued := int64(29)
	actualCIssued := <-bw.cIssued
	bw.cIssued <- actualCIssued
	if actualCIssued != expectedcIssued {
		t.Errorf("Expected cIssued credits to be %d, got %d", expectedcIssued, actualCIssued)
	}

	// Delay is still less, so should increment,
	// cTotal goes to 32
	bw.rttUpdate()

	cTotalExpected := int64(32)
	if bw.cTotal != cTotalExpected {
		t.Errorf("Expected cTotal to be %d, got %d", cTotalExpected, bw.cTotal)
	}

	// cIssued < cTotal, so should increment,
	// cOvercommit is 32-29 / 2 = 1.5, so should round up to 2
	// cAvail = 3
	// Takes min(5+2, 9+3) = 7
	go bw.updateCreditsToIssue(clientId1, 5)

	// Wait for go-routine to complete
	time.Sleep(200 * time.Millisecond)

	var expectedCreditsClient1 int64 = 5 + 2
	// the total issued credits is 20 + 7
	expectedcIssued = 27

	actualCIssued = <-bw.cIssued
	bw.cIssued <- actualCIssued

	c1, ok := bw.clientMap.Load(clientId1)
	if !ok {
		t.Errorf("Expected client1 to be in clientMap")
	}
	actualCreditsClient1 := c1.(Connection).issued

	actualId := c1.(Connection).id
	if actualId != clientId1 {
		t.Errorf("Expected client1 id to be %s, got %s", clientId1, actualId)
	}

	if actualCreditsClient1 != expectedCreditsClient1 {
		t.Errorf("Expected client1 credits to be %d, got %d", expectedCreditsClient1, actualCreditsClient1)
	}

	if actualCIssued != expectedcIssued {
		t.Errorf("Expected cIssued credits to be %d, got %d", expectedcIssued, actualCIssued)
	}
}

/*
How to test the entire workflow?
*/
