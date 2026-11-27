package act

// SetVehicle gives the actuator a read of what the unit rides, as a unique ID
// or 0. Belief keeps it because the link can arrive before the actuator
// exists.
func (a *Actuator) SetVehicle(vehicle func() int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.vehicle = vehicle
}

// Riding is the vehicle the last input was sent seated on, or 0.
func (a *Actuator) Riding() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.riding
}

func (a *Actuator) vehicleLocked() int64 {
	if a.vehicle == nil {
		return 0
	}
	return a.vehicle()
}
