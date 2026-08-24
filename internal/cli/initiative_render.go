package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func renderInitiativeList(destination io.Writer, list application.InitiativeList) error {
	if err := writeTable(destination, func(table *tabwriter.Writer) error {
		if _, err := fmt.Fprintln(table, "INITIATIVE\tSTATE\tCOMPONENTS\tTASKS\tUPDATED"); err != nil {
			return err
		}
		for _, initiative := range list.Initiatives {
			if _, err := fmt.Fprintf(table, "%s\t%s\t%d\t%d\t%s\n",
				initiative.InitiativeHandle, initiative.State, initiative.ComponentCount,
				initiative.TaskCount, initiative.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z")); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if list.NextCursor != "" {
		_, err := fmt.Fprintf(destination, "resume with --after %s\n", list.NextCursor)
		return err
	}
	return nil
}

func renderInitiativeDetail(destination io.Writer, detail application.InitiativeDetail) error {
	return writeTable(destination, func(table *tabwriter.Writer) error {
		if _, err := fmt.Fprintln(table, "INITIATIVE\tSTATE\tCOMPONENTS\tTASKS\tSTATE VERSION\tNEXT"); err != nil {
			return err
		}
		_, err := fmt.Fprintf(table, "%s\t%s\t%d\t%d\t%d\t%s\n",
			detail.Initiative.Handle, detail.Initiative.State, len(detail.Initiative.Components),
			len(detail.Graph.Nodes), detail.StateVersion, joinInitiativeActions(detail.NextSafeActions))
		return err
	})
}

func renderInitiativeExplanation(destination io.Writer, detail application.InitiativeDetail) error {
	return writeTable(destination, func(table *tabwriter.Writer) error {
		if _, err := fmt.Fprintln(table, "INITIATIVE\tSTATE\tREASON\tEXPLANATION\tNEXT"); err != nil {
			return err
		}
		_, err := fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n",
			detail.Initiative.Handle, detail.Initiative.State, detail.ReasonCode,
			detail.Explanation, joinInitiativeActions(detail.NextSafeActions))
		return err
	})
}

func renderInitiativeGraph(destination io.Writer, graph application.InitiativeGraphView) error {
	return writeTable(destination, func(table *tabwriter.Writer) error {
		if _, err := fmt.Fprintln(table, "TASK\tCOMPONENT\tREPOSITORY\tSTATE\tDEPENDENCY READY\tINTEGRATION OWNER"); err != nil {
			return err
		}
		for _, node := range graph.Nodes {
			if _, err := fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%t\t%t\n",
				node.TaskHandle, node.ComponentHandle, node.RepositoryID, node.State,
				node.DependencyReady, node.IntegrationOwner); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(table, "\nFROM\tTO\tKIND\tREQUIRED ARTIFACT"); err != nil {
			return err
		}
		for _, edge := range graph.Edges {
			if _, err := fmt.Fprintf(table, "%s\t%s\t%s\t%s\n",
				edge.From, edge.To, edge.Kind, edge.RequiredArtifactKind); err != nil {
				return err
			}
		}
		return nil
	})
}

func joinInitiativeActions(actions []application.InitiativeNextAction) string {
	if len(actions) == 0 {
		return "unknown"
	}
	values := make([]string, 0, len(actions))
	for _, action := range actions {
		values = append(values, string(action))
	}
	return strings.Join(values, ",")
}
