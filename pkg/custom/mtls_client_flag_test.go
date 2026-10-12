package custom

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestMTLSClientFlagEnvironmentDoesNotConsumeExplicitValue(t *testing.T) {
	t.Setenv("OPENAI_TEST_MTLS_FILE", "environment.pem")
	flag := &mtlsClientFlag{StringFlag: cli.StringFlag{
		Name: "certificate", Sources: cli.EnvVars("OPENAI_TEST_MTLS_FILE"), TakesFile: true,
	}}
	require.NoError(t, flag.PreParse())
	require.NoError(t, flag.PostParse())
	require.Equal(t, "environment.pem", flag.Get())
	require.True(t, flag.IsSet())
	require.NoError(t, flag.Set("certificate", ""))
	require.NoError(t, flag.PostParse())
	require.Equal(t, "", flag.Get())
	require.EqualError(t, flag.Set("certificate", "duplicate.pem"), "can't duplicate this flag")
	require.Equal(t, "", flag.Get())
	require.Same(t, &flag.StringFlag, flag.CLIStringFlag())
	require.True(t, flag.TakesFile)
	require.Equal(t, []string{"OPENAI_TEST_MTLS_FILE"}, flag.GetEnvVars())
	require.NoError(t, flag.PreParse())
	require.NoError(t, flag.Set("certificate", "next-run.pem"))
	require.Equal(t, "next-run.pem", flag.Get())
	require.EqualError(t, flag.Set("certificate", "duplicate.pem"), "can't duplicate this flag")
}
