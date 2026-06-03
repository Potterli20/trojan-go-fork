package quic

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestApplyCongestionControl_BBRAlgorithm(t *testing.T) {
	tests := []struct {
		name         string
		config       CongestionConfig
		role         string
		expectedAlgo string
		expectedSuccess bool
	}{
		{
			name: "BBR algorithm explicit",
			config: CongestionConfig{
				Algorithm:  "bbr",
				BrutalUp:   0,
				BrutalDown: 0,
			},
			role:             "server",
			expectedAlgo:     "bbr",
			expectedSuccess:  true,
		},
		{
			name: "BBR algorithm default when empty",
			config: CongestionConfig{
				Algorithm:  "",
				BrutalUp:   0,
				BrutalDown: 0,
			},
			role:             "client",
			expectedAlgo:     "bbr",
			expectedSuccess:  true,
		},
		{
			name: "BBR algorithm default when nil",
			config: CongestionConfig{
				BrutalUp:   1000,
				BrutalDown: 1000,
			},
			role:             "server",
			expectedAlgo:     "bbr",
			expectedSuccess:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := ApplyCongestionControl(nil, tt.config, tt.role)
			
			assert.Equal(t, tt.expectedAlgo, status.Algorithm, "Algorithm mismatch")
			assert.Equal(t, tt.expectedSuccess, status.Success, "Success status mismatch")
			assert.Equal(t, tt.role, status.Role, "Role mismatch")
			assert.Equal(t, tt.config.BrutalUp, status.BrutalUp, "BrutalUp mismatch")
			assert.Equal(t, tt.config.BrutalDown, status.BrutalDown, "BrutalDown mismatch")
		})
	}
}

func TestApplyCongestionControl_RenoAlgorithm(t *testing.T) {
	config := CongestionConfig{
		Algorithm:  "reno",
		BrutalUp:   0,
		BrutalDown: 0,
	}
	
	status := ApplyCongestionControl(nil, config, "client")
	
	assert.Equal(t, "reno", status.Algorithm)
	assert.True(t, status.Success)
	assert.Equal(t, uint64(0), status.EffectiveSpeed)
	assert.Empty(t, status.ErrorMessage)
}

func TestApplyCongestionControl_BrutalAlgorithm(t *testing.T) {
	tests := []struct {
		name             string
		config           CongestionConfig
		role             string
		expectedSuccess  bool
		expectedSpeed    uint64
		expectedError    string
	}{
		{
			name: "Brutal with valid up and down",
			config: CongestionConfig{
				Algorithm:  "brutal",
				BrutalUp:   1000000,
				BrutalDown: 2000000,
			},
			role:            "server",
			expectedSuccess: true,
			expectedSpeed:   1000000,
			expectedError:   "",
		},
		{
			name: "Brutal with equal up and down",
			config: CongestionConfig{
				Algorithm:  "brutal",
				BrutalUp:   5000000,
				BrutalDown: 5000000,
			},
			role:            "client",
			expectedSuccess: true,
			expectedSpeed:   5000000,
			expectedError:   "",
		},
		{
			name: "Brutal missing down",
			config: CongestionConfig{
				Algorithm:  "brutal",
				BrutalUp:   1000000,
				BrutalDown: 0,
			},
			role:            "server",
			expectedSuccess: false,
			expectedSpeed:   0,
			expectedError:   "Brutal congestion control requires both brutal_up and brutal_down to be set",
		},
		{
			name: "Brutal missing up",
			config: CongestionConfig{
				Algorithm:  "brutal",
				BrutalUp:   0,
				BrutalDown: 1000000,
			},
			role:            "client",
			expectedSuccess: false,
			expectedSpeed:   0,
			expectedError:   "Brutal congestion control requires both brutal_up and brutal_down to be set",
		},
		{
			name: "Brutal missing both",
			config: CongestionConfig{
				Algorithm:  "brutal",
				BrutalUp:   0,
				BrutalDown: 0,
			},
			role:            "server",
			expectedSuccess: false,
			expectedSpeed:   0,
			expectedError:   "Brutal congestion control requires both brutal_up and brutal_down to be set",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := ApplyCongestionControl(nil, tt.config, tt.role)
			
			assert.Equal(t, tt.expectedSuccess, status.Success, "Success status mismatch")
			assert.Equal(t, tt.expectedSpeed, status.EffectiveSpeed, "EffectiveSpeed mismatch")
			assert.Equal(t, tt.expectedError, status.ErrorMessage, "ErrorMessage mismatch")
		})
	}
}

func TestApplyCongestionControl_ForceBrutalAlgorithm(t *testing.T) {
	tests := []struct {
		name            string
		config          CongestionConfig
		role            string
		expectedSuccess bool
		expectedSpeed   uint64
		expectedError   string
	}{
		{
			name: "Force-brutal with valid up",
			config: CongestionConfig{
				Algorithm:  "force-brutal",
				BrutalUp:   1000000,
				BrutalDown: 0,
			},
			role:            "server",
			expectedSuccess: true,
			expectedSpeed:   1000000,
			expectedError:   "",
		},
		{
			name: "Force-brutal with both values",
			config: CongestionConfig{
				Algorithm:  "force-brutal",
				BrutalUp:   5000000,
				BrutalDown: 3000000,
			},
			role:            "client",
			expectedSuccess: true,
			expectedSpeed:   5000000,
			expectedError:   "",
		},
		{
			name: "Force-brutal missing up",
			config: CongestionConfig{
				Algorithm:  "force-brutal",
				BrutalUp:   0,
				BrutalDown: 1000000,
			},
			role:            "server",
			expectedSuccess: false,
			expectedSpeed:   0,
			expectedError:   "Force-brutal congestion control requires brutal_up to be set",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := ApplyCongestionControl(nil, tt.config, tt.role)
			
			assert.Equal(t, tt.expectedSuccess, status.Success, "Success status mismatch")
			assert.Equal(t, tt.expectedSpeed, status.EffectiveSpeed, "EffectiveSpeed mismatch")
			assert.Equal(t, tt.expectedError, status.ErrorMessage, "ErrorMessage mismatch")
		})
	}
}

func TestApplyCongestionControl_UnknownAlgorithm(t *testing.T) {
	tests := []struct {
		name           string
		algorithm      string
		expectedError  string
	}{
		{
			name:          "Unknown algorithm CUBIC",
			algorithm:     "cubic",
			expectedError: "Unknown congestion control: cubic",
		},
		{
			name:          "Unknown algorithm VEGAS",
			algorithm:     "vegas",
			expectedError: "Unknown congestion control: vegas",
		},
		{
			name:          "Empty string defaults to BBR",
			algorithm:     "",
			expectedError: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := CongestionConfig{
				Algorithm: tt.algorithm,
			}
			status := ApplyCongestionControl(nil, config, "server")
			
			if tt.expectedError != "" {
				assert.False(t, status.Success, "Expected failure for unknown algorithm")
				assert.Equal(t, tt.expectedError, status.ErrorMessage, "ErrorMessage mismatch")
			} else {
				assert.True(t, status.Success, "Expected success")
				assert.Equal(t, "bbr", status.Algorithm, "Default algorithm should be BBR")
			}
		})
	}
}

func TestApplyCongestionControl_BoundaryConditions(t *testing.T) {
	tests := []struct {
		name            string
		config          CongestionConfig
		role            string
		expectedSuccess bool
		expectedSpeed   uint64
	}{
		{
			name: "Zero speed values",
			config: CongestionConfig{
				Algorithm:  "brutal",
				BrutalUp:   0,
				BrutalDown: 0,
			},
			role:            "server",
			expectedSuccess: false,
			expectedSpeed:   0,
		},
		{
			name: "Maximum uint64 values",
			config: CongestionConfig{
				Algorithm:  "brutal",
				BrutalUp:   18446744073709551615,
				BrutalDown: 18446744073709551615,
			},
			role:            "client",
			expectedSuccess: true,
			expectedSpeed:   18446744073709551615,
		},
		{
			name: "Different boundary values",
			config: CongestionConfig{
				Algorithm:  "brutal",
				BrutalUp:   1,
				BrutalDown: 18446744073709551615,
			},
			role:            "server",
			expectedSuccess: true,
			expectedSpeed:   1,
		},
		{
			name: "Very small non-zero values",
			config: CongestionConfig{
				Algorithm:  "brutal",
				BrutalUp:   100,
				BrutalDown: 200,
			},
			role:            "client",
			expectedSuccess: true,
			expectedSpeed:   100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := ApplyCongestionControl(nil, tt.config, tt.role)
			
			assert.Equal(t, tt.expectedSuccess, status.Success, "Success status mismatch")
			assert.Equal(t, tt.expectedSpeed, status.EffectiveSpeed, "EffectiveSpeed mismatch")
		})
	}
}

func TestApplyCongestionControl_RoleParameter(t *testing.T) {
	tests := []struct {
		name     string
		role     string
	}{
		{
			name: "Server role",
			role: "server",
		},
		{
			name: "Client role",
			role: "client",
		},
		{
			name: "Empty role",
			role: "",
		},
		{
			name: "Custom role",
			role: "proxy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := CongestionConfig{
				Algorithm: "bbr",
			}
			status := ApplyCongestionControl(nil, config, tt.role)
			
			assert.Equal(t, tt.role, status.Role, "Role should be preserved")
			assert.True(t, status.Success, "Should succeed with BBR")
		})
	}
}

func TestMinUint64(t *testing.T) {
	tests := []struct {
		name     string
		a        uint64
		b        uint64
		expected uint64
	}{
		{
			name:     "a less than b",
			a:        100,
			b:        200,
			expected: 100,
		},
		{
			name:     "a greater than b",
			a:        200,
			b:        100,
			expected: 100,
		},
		{
			name:     "a equals b",
			a:        150,
			b:        150,
			expected: 150,
		},
		{
			name:     "a is zero",
			a:        0,
			b:        100,
			expected: 0,
		},
		{
			name:     "b is zero",
			a:        100,
			b:        0,
			expected: 0,
		},
		{
			name:     "both zero",
			a:        0,
			b:        0,
			expected: 0,
		},
		{
			name:     "maximum values",
			a:        18446744073709551615,
			b:        18446744073709551614,
			expected: 18446744073709551614,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := minUint64(tt.a, tt.b)
			assert.Equal(t, tt.expected, result, "minUint64 result mismatch")
		})
	}
}

func TestCongestionStatusStruct(t *testing.T) {
	status := CongestionStatus{
		Algorithm:      "bbr",
		Role:           "server",
		BrutalUp:       1000000,
		BrutalDown:     2000000,
		EffectiveSpeed: 1000000,
		Success:        true,
		ErrorMessage:   "",
	}

	assert.Equal(t, "bbr", status.Algorithm)
	assert.Equal(t, "server", status.Role)
	assert.Equal(t, uint64(1000000), status.BrutalUp)
	assert.Equal(t, uint64(2000000), status.BrutalDown)
	assert.Equal(t, uint64(1000000), status.EffectiveSpeed)
	assert.True(t, status.Success)
	assert.Empty(t, status.ErrorMessage)
}

func TestCongestionConfigStruct(t *testing.T) {
	config := CongestionConfig{
		Algorithm:  "brutal",
		BrutalUp:   5000000,
		BrutalDown: 3000000,
	}

	assert.Equal(t, "brutal", config.Algorithm)
	assert.Equal(t, uint64(5000000), config.BrutalUp)
	assert.Equal(t, uint64(3000000), config.BrutalDown)
}
