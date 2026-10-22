package images

import (
	"context"
	"io"

	"github.com/openai/openai-cli/internal/imagemodels"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/urfave/cli/v3"
)

// Test-only access to private feature state for direct integration tests.
type TestLoadingTestWriter = loadingTestWriter

var TestIsTerminal = isTerminal
var TestImagePickerTabChoicePath = imagePickerTabChoicePath
var TestImagePickerSelectShellTargets = imagePickerSelectShellTargets
var TestRegisterImageSavingFlags = registerImageSavingFlags
var TestFirstRunFileSnapshot = firstRunFileSnapshot
var TestImageInlinePreferenceCommands = imageInlinePreferenceCommands
var TestImageSavingWorkflow = imageSavingWorkflow
var TestSetupImagePickerFirstRun = setupImagePickerFirstRun
var TestImageModelTestRow = imageModelTestRow
var TestImageInlinePreferenceLocation = imageInlinePreferenceLocation
var TestImagePickerShellTarget = imagePickerShellTarget

type TestImageModelsReport = imageModelsReport

var TestPickerShellSetupHome = pickerShellSetupHome
var TestWriteImageModelsData = writeImageModelsData
var TestImagePickerTabDeclined = imagePickerTabDeclined
var TestImageInlinePreferencePath = imageInlinePreferencePath
var TestLegacyImagePickerTabChoicePath = legacyImagePickerTabChoicePath
var TestDeclineImagePickerTab = declineImagePickerTab
var TestFinishInteractiveImagePreview = finishInteractiveImagePreview
var TestPrivateImagePickerState = privateImagePickerState

type TestOutputJSON = outputJSON

var TestImagePickerStatePath = imagePickerStatePath
var TestImagePickerDisplayFailure = imagePickerDisplayFailure

func NewOutputPlanForTest(directory, name, inline string) *imageOutputPlan {
	return &imageOutputPlan{directory: directory, name: name, inline: inline}
}
func (p *imageOutputPlan) DirectoryForTest() string { return p.directory }
func (p *imageOutputPlan) SaveForTest(ctx context.Context, data []byte, out io.Writer) error {
	return p.save(ctx, data, out)
}
func (p *imageOutputPlan) SetLoadingStageForTest(stage func(clioutput.LoadingStage)) {
	p.loadingStage = stage
}
func WithPresentationForTest(ctx context.Context, plan *imageOutputPlan, out io.Writer) context.Context {
	return context.WithValue(ctx, imagePresentationKey{}, imagePresentation{plan, out})
}
func InlineForTest(ctx context.Context) string {
	return ctx.Value(imagePresentationKey{}).(imagePresentation).plan.inline
}
func PreparedBodyForTest(command *cli.Command) io.ReadCloser {
	return command.Metadata[imageMultipartMetadata].(*imageMultipartPreparation).body.(io.ReadCloser)
}
func InterruptForTest(err error) error      { return &imageLoadingInterrupt{err} }
func InteractiveExitForTest(code int) error { return interactiveImageExit{code} }
func ModelsErrorForTest(invocation string, extra bool, results []imagemodels.Result) *ModelsError {
	return &ModelsError{invocation: invocation, extraArguments: extra, results: results}
}
func ShellPathsForTest(home, config, zdotdir string) imagePickerShellPaths {
	return imagePickerShellPaths{home: home, config: config, zdotdir: zdotdir}
}
