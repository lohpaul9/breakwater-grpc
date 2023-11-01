package breakwater

import (
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
)

var defaultSLO int64 = BWParametersDefault.SLO
var targetThreshold float64 = float64(defaultSLO) * DELAY_THRESHOLD_PERCENT

// Test getDelay
func TestGetDelay(t *testing.T) {
	bw := InitBreakwater(BWParametersDefault)
	<-bw.currGreatestDelay
	<-bw.prevGreatestDelay
	bw.currGreatestDelay <- 100
	bw.prevGreatestDelay <- 500.0
	delay := bw.getDelay()
	if delay != 500.0 {
		t.Errorf("Expected delay to be 500.0, got %f", delay)
	}
}

func TestGetAdditiveFactor(t *testing.T) {
	bw := InitBreakwater(BWParametersDefault)
	<-bw.numClients
	bw.numClients <- 100
	aFactor := bw.getAdditiveFactor()
	expected := max(roundedInt(100*0.001), 1)
	if aFactor != expected {
		t.Errorf("Expected aFactor to be %d, got %d", expected, aFactor)
	}
}

func TestGetMultiplicativeFactor(t *testing.T) {
	bw := InitBreakwater(BWParametersDefault)
	// Target threshold should be 160 * 0.4 = 64
	<-bw.currGreatestDelay
	<-bw.prevGreatestDelay
	bw.currGreatestDelay <- 300
	bw.prevGreatestDelay <- 100
	mFactor := bw.getMultiplicativeFactor(bw.getDelay())
	expected := math.Max(1.0-BWParametersDefault.bFactor*((300.0-targetThreshold)/targetThreshold), 0.5)
	if mFactor != expected {
		t.Errorf("Expected mFactor to be %f, got %f", expected, mFactor)
	}
}

func TestRegisterClient(t *testing.T) {
	bw := InitBreakwater(BWParametersDefault)
	var counter chan int64 = make(chan int64, 20)
	reg := func() {
		// Generate UUID for client
		id := uuid.New()
		bw.RegisterClient(id, 30)
		counter <- 1
	}
	for i := 0; i < 20; i++ {
		go reg()
	}
	for i := 0; i < 20; i++ {
		<-counter
	}

	numClients := <-bw.numClients
	bw.numClients <- numClients
	if numClients != 20 {
		t.Errorf("Expected 20 clients, got %d", bw.numClients)
	}
}

func TestGetTotalCreditIncrement(t *testing.T) {
	bw := InitBreakwater(BWParametersDefault)
	var numClients int64 = 10000
	<-bw.numClients
	bw.numClients <- numClients
	// Both of these are below SLO threshold of 160 * 0.4 = 64
	<-bw.currGreatestDelay
	<-bw.prevGreatestDelay
	bw.currGreatestDelay <- 20
	bw.prevGreatestDelay <- 10
	totalCredits := bw.getUpdatedTotalCredits()
	expected := BWParametersDefault.InitialCredits + max(roundedInt(float64(numClients)*0.001), 1)
	if totalCredits != expected {
		t.Errorf("Expected totalCredits to be %d, got %d", expected, totalCredits)
	}
}

func TestGetTotalCreditDecrement(t *testing.T) {
	bw := InitBreakwater(BWParametersDefault)
	// Both of these are below SLO threshold of 160 * 0.4 = 64
	<-bw.currGreatestDelay
	<-bw.prevGreatestDelay
	bw.currGreatestDelay <- 500
	bw.prevGreatestDelay <- 30
	totalCredits := bw.getUpdatedTotalCredits()
	expectedMultFact := math.Max(1.0-BWParametersDefault.bFactor*((500.0-targetThreshold)/targetThreshold), 0.5)
	expected := roundedInt(float64(BWParametersDefault.InitialCredits) * expectedMultFact)
	if totalCredits != expected {
		t.Errorf("Expected totalCredits to be %d, got %d", expected, totalCredits)
	}
}

func TestRTTUpdateIncrement(t *testing.T) {
	bw := InitBreakwater(BWParametersDefault)
	var numClients int64 = 600
	<-bw.numClients
	bw.numClients <- numClients

	// Both of these are below SLO threshold of 160 * 0.4 = 64
	<-bw.currGreatestDelay
	<-bw.prevGreatestDelay
	bw.currGreatestDelay <- 60
	bw.prevGreatestDelay <- 30
	time.Sleep(400 * time.Millisecond)
	bw.rttUpdate()
	time.Sleep(400 * time.Millisecond)
	bw.rttUpdate()
	totalCredits := bw.cTotal
	expected := BWParametersDefault.InitialCredits + max(roundedInt(float64(numClients)*0.001), 1)*2
	if totalCredits != expected {
		t.Errorf("Expected totalCredits to be %d, got %d", expected, totalCredits)
	}
}

func TestRttUpdateDecrement(t *testing.T) {
	bw := InitBreakwater(BWParametersDefault)

	// Both of these are below SLO threshold of 160 * 0.4 = 64
	<-bw.currGreatestDelay
	<-bw.prevGreatestDelay
	bw.currGreatestDelay <- 500
	bw.prevGreatestDelay <- 30
	time.Sleep(400 * time.Millisecond)
	bw.rttUpdate()

	// Both of these are below SLO threshold of 160 * 0.4 = 64
	<-bw.currGreatestDelay
	<-bw.prevGreatestDelay
	bw.currGreatestDelay <- 300
	bw.prevGreatestDelay <- 30
	time.Sleep(400 * time.Millisecond)
	bw.rttUpdate()

	totalCredits := bw.cTotal

	// Calculate expected
	expectedMultFact := math.Max(1.0-BWParametersDefault.bFactor*((500.0-targetThreshold)/targetThreshold), 0.5)
	expected := roundedInt(float64(BWParametersDefault.InitialCredits) * expectedMultFact)
	expectedMultFact = math.Max(1.0-BWParametersDefault.bFactor*((300.0-targetThreshold)/targetThreshold), 0.5)
	expected = roundedInt(float64(expected) * expectedMultFact)

	if totalCredits != expected {
		t.Errorf("Expected totalCredits to be %d, got %d", expected, totalCredits)
	}
}

func TestRttUpdateNotReached(t *testing.T) {
	bw := InitBreakwater(BWParametersDefault)

	bw.rttUpdate()

	totalCredits := bw.cTotal
	expected := BWParametersDefault.InitialCredits

	if totalCredits != expected {
		t.Errorf("Expected totalCredits to be %d, got %d", expected, totalCredits)
	}
}

func TestRTTUpdateOnlyOnceRace(t *testing.T) {
	bw := InitBreakwater(BWParametersDefault)
	var numClients int64 = 600
	<-bw.numClients
	bw.numClients <- numClients

	// Both of these are below SLO threshold of 160 * 0.4 = 64
	<-bw.currGreatestDelay
	<-bw.prevGreatestDelay
	bw.currGreatestDelay <- 60
	bw.prevGreatestDelay <- 30
	time.Sleep(400 * time.Millisecond)
	bw.rttUpdate()
	bw.rttUpdate()
	totalCredits := bw.cTotal
	expected := BWParametersDefault.InitialCredits + max(roundedInt(float64(numClients)*0.001), 1)
	if totalCredits != expected {
		t.Errorf("Expected totalCredits to be %d, got %d", expected, totalCredits)
	}
}

// Test checks if cIssued updated
